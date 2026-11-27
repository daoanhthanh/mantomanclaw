package backends

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"golang.org/x/crypto/ssh"
)

const testPassword = "test-pw"

// sshTestServer is an in-process SSH server. As a target it runs "exec" requests;
// with proxy set it also forwards direct-tcpip channels (a jump host); with hang set
// it accepts TCP and never speaks SSH.
type sshTestServer struct {
	t      *testing.T
	ln     net.Listener
	signer ssh.Signer
	cfg    *ssh.ServerConfig
	proxy  bool
	hang   bool

	accepted atomic.Int32 // TCP connections accepted
	closed   atomic.Int32 // connections whose handler finished
	tunnels  atomic.Int32 // direct-tcpip channels opened

	mu    sync.Mutex
	conns []net.Conn
}

func newSSHTestServer(t *testing.T, proxy, hang bool) *sshTestServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == testPassword {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshTestServer{t: t, ln: ln, signer: signer, cfg: cfg, proxy: proxy, hang: hang}
	go s.acceptLoop()
	t.Cleanup(s.stop)
	return s
}

func (s *sshTestServer) fingerprint() string { return ssh.FingerprintSHA256(s.signer.PublicKey()) }
func (s *sshTestServer) port() int           { return s.ln.Addr().(*net.TCPAddr).Port }
func (s *sshTestServer) addr() string        { return s.ln.Addr().String() }

func (s *sshTestServer) stop() {
	_ = s.ln.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

func (s *sshTestServer) acceptLoop() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.accepted.Add(1)
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.handle(c)
	}
}

func (s *sshTestServer) handle(c net.Conn) {
	defer s.closed.Add(1)
	if s.hang {
		_, _ = io.Copy(io.Discard, c)
		return
	}
	sc, chans, reqs, err := ssh.NewServerConn(c, s.cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	defer sc.Close()
	go func() {
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(true, nil) // answers keepalive@openssh.com
			}
		}
	}()
	go func() {
		for nc := range chans {
			switch nc.ChannelType() {
			case "session":
				go s.session(nc)
			case "direct-tcpip":
				if !s.proxy {
					_ = nc.Reject(ssh.Prohibited, "no forwarding")
					continue
				}
				go s.forward(nc)
			default:
				_ = nc.Reject(ssh.UnknownChannelType, "unsupported")
			}
		}
	}()
	_ = sc.Wait()
}

func (s *sshTestServer) session(nc ssh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	for r := range reqs {
		if r.Type != "exec" {
			_ = r.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		_ = ssh.Unmarshal(r.Payload, &p)
		_ = r.Reply(true, nil)
		_, _ = io.WriteString(ch, "ran:"+p.Command)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
		return
	}
}

func (s *sshTestServer) forward(nc ssh.NewChannel) {
	var d struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &d); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	up, err := net.Dial("tcp", net.JoinHostPort(d.Host, strconv.Itoa(int(d.Port))))
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = up.Close()
		return
	}
	s.tunnels.Add(1)
	go ssh.DiscardRequests(reqs)
	go func() { _, _ = io.Copy(up, ch); _ = up.Close() }()
	go func() { _, _ = io.Copy(ch, up); _ = ch.Close() }()
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func targetMeta(target *sshTestServer) *store.SSHMetadata {
	return &store.SSHMetadata{
		Host:                  "127.0.0.1",
		Port:                  target.port(),
		User:                  "u",
		Password:              testPassword,
		KnownHostsFingerprint: target.fingerprint(),
		ConnectTimeoutSec:     2,
	}
}

func withJump(m *store.SSHMetadata, jump *sshTestServer) *store.SSHMetadata {
	m.JumpHost = &store.SSHJumpHost{
		Host:                  "127.0.0.1",
		Port:                  jump.port(),
		User:                  "j",
		Password:              testPassword,
		KnownHostsFingerprint: jump.fingerprint(),
	}
	return m
}

func newTestPool(t *testing.T) *clientPool {
	t.Helper()
	p := newClientPool()
	t.Cleanup(p.Close)
	return p
}

func runExec(t *testing.T, c *ssh.Client, cmd string) string {
	t.Helper()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()
	out, err := sess.Output(cmd)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	return string(out)
}

func TestDialSSH_ThroughJump(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)

	conn, err := dialSSH(context.Background(), withJump(targetMeta(target), jump), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if got := runExec(t, conn.client, "hello"); got != "ran:hello" {
		t.Fatalf("exec output = %q", got)
	}
	if jump.tunnels.Load() != 1 {
		t.Fatalf("jump tunnels = %d, want 1 (target must be reached via direct-tcpip)", jump.tunnels.Load())
	}
	if target.accepted.Load() != 1 {
		t.Fatalf("target connections = %d, want 1", target.accepted.Load())
	}
}

func TestDialSSH_DirectRegression(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	p := newTestPool(t)
	ws := &store.Workstation{ID: uuid.New(), WorkstationKey: "direct"}

	client, release, err := p.Get(context.Background(), ws, targetMeta(target), nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer release()
	if got := runExec(t, client, "x"); got != "ran:x" {
		t.Fatalf("exec output = %q", got)
	}
	if pc := p.clients[ws.ID][0]; pc.conn.jump != nil || pc.conn.closeJump != nil {
		t.Fatal("direct connection must not carry a jump client")
	}
}

func TestDialSSH_JumpHostKeyPinned(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)
	m := withJump(targetMeta(target), jump)
	m.JumpHost.KnownHostsFingerprint = "SHA256:wrong"

	_, err := dialSSH(context.Background(), m, nil)
	if !errors.Is(err, errHostKeyMismatch) {
		t.Fatalf("err = %v, want errHostKeyMismatch", err)
	}
	var he *hopError
	if !errors.As(err, &he) || he.hop != hopJump {
		t.Fatalf("err = %v, want attribution to the jump hop", err)
	}
	if n := target.accepted.Load(); n != 0 {
		t.Fatalf("target saw %d connections, want 0", n)
	}
}

func TestDialSSH_JumpHostKeyRequired(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)
	m := withJump(targetMeta(target), jump)
	m.JumpHost.KnownHostsFingerprint = "" // TOFU must be refused for the jump hop

	if _, err := dialSSH(context.Background(), m, nil); !errors.Is(err, errHostKeyMismatch) {
		t.Fatalf("err = %v, want errHostKeyMismatch", err)
	}
	if n := target.accepted.Load(); n != 0 {
		t.Fatalf("target saw %d connections, want 0", n)
	}
}

func TestDialSSH_TargetHostKeyPinnedClosesJump(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)
	m := withJump(targetMeta(target), jump)
	m.KnownHostsFingerprint = "SHA256:wrong"

	_, err := dialSSH(context.Background(), m, nil)
	if !errors.Is(err, errHostKeyMismatch) {
		t.Fatalf("err = %v, want errHostKeyMismatch", err)
	}
	var he *hopError
	if !errors.As(err, &he) || he.hop != hopTarget {
		t.Fatalf("err = %v, want attribution to the target hop", err)
	}
	waitFor(t, 2*time.Second, "jump connection to close", func() bool { return jump.closed.Load() == 1 })
}

func TestDialSSH_TargetHandshakeHangIsBounded(t *testing.T) {
	target := newSSHTestServer(t, false, true) // accepts TCP, never speaks SSH
	jump := newSSHTestServer(t, true, false)
	m := withJump(targetMeta(target), jump)
	m.ConnectTimeoutSec = 1

	start := time.Now()
	_, err := dialSSH(context.Background(), m, nil)
	if err == nil {
		t.Fatal("expected handshake timeout")
	}
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("dial took %s, want it bounded by ConnectTimeoutSec", el)
	}
	waitFor(t, 2*time.Second, "jump connection to close", func() bool { return jump.closed.Load() == 1 })
}

func TestDialSSH_SSRFBlockedNeverConnects(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)

	orig := lookupHost
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host == "evil.example" {
			return []string{"169.254.169.254"}, nil
		}
		return orig(context.Background(), host)
	}
	t.Cleanup(func() { lookupHost = orig })

	t.Run("jump host resolving to link-local", func(t *testing.T) {
		m := withJump(targetMeta(target), jump)
		m.JumpHost.Host = "evil.example"
		_, err := dialSSH(context.Background(), m, nil)
		if !errors.Is(err, errWorkstationSSRFBlocked) {
			t.Fatalf("err = %v, want errWorkstationSSRFBlocked", err)
		}
		if jump.accepted.Load() != 0 || target.accepted.Load() != 0 {
			t.Fatal("a connection was made to a blocked hop")
		}
	})

	t.Run("direct target resolving to link-local", func(t *testing.T) {
		m := targetMeta(target)
		m.Host = "evil.example"
		_, err := dialSSH(context.Background(), m, nil)
		if !errors.Is(err, errWorkstationSSRFBlocked) {
			t.Fatalf("err = %v, want errWorkstationSSRFBlocked", err)
		}
		if target.accepted.Load() != 0 {
			t.Fatal("a connection was made to a blocked target")
		}
	})

	t.Run("literal link-local address", func(t *testing.T) {
		m := targetMeta(target)
		m.Host = "169.254.169.254"
		if _, err := dialSSH(context.Background(), m, nil); !errors.Is(err, errWorkstationSSRFBlocked) {
			t.Fatalf("err = %v, want errWorkstationSSRFBlocked", err)
		}
	})
}

func TestCountsTowardCircuit(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	m := targetMeta(target)
	m.Password = "wrong"
	_, authErr := dialSSH(context.Background(), m, nil)
	if authErr == nil {
		t.Fatal("expected auth failure")
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"real auth failure", authErr, true},
		{"host key mismatch", errHostKeyMismatch, true},
		{"ssrf block", &ssrfError{msg: "x"}, false},
		{"network error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, false},
		{"context deadline", context.DeadlineExceeded, false},
	}
	for _, tc := range tests {
		if got := countsTowardCircuit(tc.err); got != tc.want {
			t.Errorf("%s: countsTowardCircuit = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPool_CloseOrderTargetBeforeJump(t *testing.T) {
	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)
	p := newTestPool(t)
	ws := &store.Workstation{ID: uuid.New(), WorkstationKey: "order"}

	_, release, err := p.Get(context.Background(), ws, withJump(targetMeta(target), jump), nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	release()

	var order []string
	conn := p.clients[ws.ID][0].conn
	closeTarget, closeJump := conn.closeTarget, conn.closeJump
	conn.closeTarget = func() error { order = append(order, "target"); return closeTarget() }
	conn.closeJump = func() error { order = append(order, "jump"); return closeJump() }

	p.CloseWorkstation(ws.ID)
	if len(order) != 2 || order[0] != "target" || order[1] != "jump" {
		t.Fatalf("close order = %v, want [target jump]", order)
	}
}

func TestPool_DeadJumpIsEvictedByKeepalive(t *testing.T) {
	origInterval, origReply := keepaliveInterval, keepaliveReplyTimeout
	keepaliveInterval, keepaliveReplyTimeout = 50*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { keepaliveInterval, keepaliveReplyTimeout = origInterval, origReply })

	target := newSSHTestServer(t, false, false)
	jump := newSSHTestServer(t, true, false)
	p := newTestPool(t)
	ws := &store.Workstation{ID: uuid.New(), WorkstationKey: "dead-jump"}
	m := withJump(targetMeta(target), jump)

	_, release, err := p.Get(context.Background(), ws, m, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	release()

	jump.stop()

	waitFor(t, 3*time.Second, "keepalive to evict the pair", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.clients[ws.ID]) == 0
	})

	start := time.Now()
	_, _, err = p.Get(context.Background(), ws, m, nil)
	if err == nil {
		t.Fatal("expected dial error once the jump is down")
	}
	var he *hopError
	if !errors.As(err, &he) || he.stage != "jump dial" {
		t.Fatalf("err = %v, want a jump dial error", err)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("Get hung for %s", el)
	}
	// A network failure must not push the workstation toward lockout.
	p.mu.Lock()
	failures := p.circuitFor(ws.ID).failures
	p.mu.Unlock()
	if failures != 0 {
		t.Fatalf("circuit failures = %d, want 0 for a network error", failures)
	}
}
