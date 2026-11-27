package backends

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/security"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"golang.org/x/crypto/ssh"
)

// dialSSH establishes a new *ssh.Client using the provided metadata and key material.
// Context cancellation aborts the dial; the spawned goroutine cleans up on its own.
func dialSSH(ctx context.Context, meta *store.SSHMetadata, keyMaterial []byte) (*ssh.Client, error) {
	timeout := time.Duration(meta.ConnectTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	targetAuth, err := buildAuthMethods(keyMaterial, meta.Password)
	if err != nil {
		return nil, err
	}
	targetCfg := &ssh.ClientConfig{
		User:            meta.User,
		Auth:            targetAuth,
		HostKeyCallback: buildHostKeyCallback(hopTarget, meta.Host, meta.KnownHostsFingerprint, false),
		Timeout:         timeout,
	}
	targetAddr := net.JoinHostPort(meta.Host, strconv.Itoa(meta.Port))

	if meta.JumpHost == nil {
		conn, err := dialPinned(ctx, hopTarget, meta.Host, meta.Port, timeout)
		if err != nil {
			return nil, &hopError{hop: hopTarget, stage: "target dial", err: err}
		}
		client, err := sshHandshake(ctx, conn, targetAddr, targetCfg, timeout)
		if err != nil {
			return nil, &hopError{hop: hopTarget, stage: "target handshake", err: err}
		}
		return newSSHConn(client, nil), nil
	}

	j := meta.JumpHost
	jumpAuth, err := buildAuthMethods([]byte(j.PrivateKey), j.Password)
	if err != nil {
		return nil, &hopError{hop: hopJump, stage: "jump dial", err: err}
	}
	jumpCfg := &ssh.ClientConfig{
		User:            j.User,
		Auth:            jumpAuth,
		HostKeyCallback: buildHostKeyCallback(hopJump, j.Host, j.KnownHostsFingerprint, true),
		Timeout:         timeout,
	}
	jumpAddr := net.JoinHostPort(j.Host, strconv.Itoa(j.Port))

	jumpTCP, err := dialPinned(ctx, hopJump, j.Host, j.Port, timeout)
	if err != nil {
		return nil, &hopError{hop: hopJump, stage: "jump dial", err: err}
	}
	jump, err := sshHandshake(ctx, jumpTCP, jumpAddr, jumpCfg, timeout)
	if err != nil {
		return nil, &hopError{hop: hopJump, stage: "jump dial", err: err}
	}

	// From here every failure must close the jump client.
	chanCtx, cancel := context.WithTimeout(ctx, timeout)
	tunnel, err := jump.DialContext(chanCtx, "tcp", targetAddr)
	cancel()
	if err != nil {
		_ = jump.Close()
		return nil, &hopError{hop: hopJump, stage: "jump->target channel", err: err}
	}
	client, err := sshHandshake(ctx, tunnel, targetAddr, targetCfg, timeout)
	if err != nil {
		_ = jump.Close()
		return nil, &hopError{hop: hopTarget, stage: "target handshake", err: err}
	}
	return newSSHConn(client, jump), nil
}

// sshHandshake runs the SSH handshake over conn. chanConn (a jump tunnel) ignores
// deadlines and ClientConfig.Timeout covers only the TCP dial, so an explicit timer
// and ctx bound the handshake; on timeout/cancel conn is closed, which unblocks it.
// On error conn is always closed.
func sshHandshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	type result struct {
		client *ssh.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{client: ssh.NewClient(c, chans, reqs)}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			_ = conn.Close()
		}
		return r.client, r.err
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	case <-timer.C:
		_ = conn.Close()
		return nil, fmt.Errorf("handshake timed out after %s", timeout)
	}
}

// dialPinned resolves host once, rejects always-blocked addresses, and dials the
// chosen IP directly so a second DNS answer cannot redirect the connection.
func dialPinned(ctx context.Context, hop, host string, port int, timeout time.Duration) (net.Conn, error) {
	ip, err := resolvePinned(ctx, hop, host)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
}

// resolvePinned returns the first address for host that is not link-local,
// cloud-metadata, multicast or unspecified. RFC 1918, loopback and ULA are allowed.
func resolvePinned(ctx context.Context, hop, host string) (string, error) {
	addrs := []string{host}
	if _, err := netip.ParseAddr(host); err != nil {
		var lerr error
		addrs, lerr = lookupHost(ctx, host)
		if lerr != nil {
			return "", fmt.Errorf("resolve %s: %w", host, lerr)
		}
		if len(addrs) == 0 {
			return "", fmt.Errorf("resolve %s: no addresses", host)
		}
	}
	var blocked string
	for _, a := range addrs {
		parsed, err := netip.ParseAddr(a)
		if err != nil {
			continue
		}
		ip := net.IP(parsed.WithZone("").Unmap().AsSlice())
		if security.IsAlwaysBlocked(ip) {
			if blocked == "" {
				blocked = a
			}
			continue
		}
		return a, nil
	}
	if blocked == "" {
		return "", fmt.Errorf("resolve %s: no usable address", host)
	}
	slog.Warn("security.workstation_ssrf_block", "hop", hop, "host", host, "ip", blocked)
	return "", &ssrfError{msg: i18n.T(i18n.DefaultLocale, i18n.MsgWorkstationSSRFBlocked, hop+" "+host+" ("+blocked+")")}
}

// buildHostKeyCallback returns an ssh.HostKeyCallback that enforces fingerprint pinning.
// TOFU policy (requirePin=false): if fingerprint is empty, accept the key and log it so
// the operator can record it. With requirePin the fingerprint must be set and match —
// used for the jump hop, which carries credentials.
// NOTE: InsecureIgnoreHostKey is never used — this is enforced by CI grep check.
func buildHostKeyCallback(hop, host, fingerprint string, requirePin bool) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if fingerprint == "" {
			if requirePin {
				slog.Warn("security.ssh_host_key_unpinned", "hop", hop, "host", host, "actual", fp)
				return fmt.Errorf("%w: no pinned fingerprint for %s %s", errHostKeyMismatch, hop, host)
			}
			slog.Info("workstation.ssh_host_key_tofu",
				"hop", hop,
				"host", host,
				"fingerprint", fp,
				"hint", "persist this fingerprint to knownHostsFingerprint for security",
			)
			return nil
		}
		if fp != fingerprint {
			slog.Warn("security.ssh_host_key_changed",
				"hop", hop,
				"host", host,
				"expected", fingerprint,
				"actual", fp,
			)
			return fmt.Errorf("%w: %s %s: expected %s got %s", errHostKeyMismatch, hop, host, fingerprint, fp)
		}
		return nil
	}
}

// buildAuthMethods constructs SSH auth methods.
// Prefers public-key auth when keyMaterial is non-empty; falls back to password.
func buildAuthMethods(keyMaterial []byte, password string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if len(keyMaterial) > 0 {
		signer, err := ssh.ParsePrivateKey(keyMaterial)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}
	if len(methods) == 0 {
		return nil, errors.New("no auth method available: provide privateKey or password")
	}
	return methods, nil
}
