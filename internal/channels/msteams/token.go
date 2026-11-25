package msteams

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	tokenEndpointFmt        = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	botConnectorScope        = "https://api.botframework.com/.default"
	tokenRefreshMargin       = 5 * time.Minute
	clientAssertionType      = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
)

// tokenSource acquires and caches OAuth2 tokens for a given (tenant, scope).
// Thread-safe; uses singleflight semantics via a mutex + inflight flag.
type tokenSource struct {
	mu         sync.Mutex
	inflight   bool
	inflightCh chan struct{}

	tenantID     string
	appID        string
	clientSecret string // empty if cert mode
	certPEM      string // X.509 PEM
	privKey      *rsa.PrivateKey
	certThumb    string // base64url(SHA-256(DER))

	// per-scope token cache
	tokens map[string]cachedToken

	httpClient *http.Client
}

type cachedToken struct {
	accessToken string
	expiresAt   time.Time
}

func newTokenSource(creds msteamsCreds) (*tokenSource, error) {
	ts := &tokenSource{
		tenantID:     creds.TenantID,
		appID:        creds.AppID,
		clientSecret: creds.ClientSecret,
		tokens:       make(map[string]cachedToken),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}

	if creds.CertificatePEM != "" && creds.PrivateKeyPEM != "" {
		privKey, thumb, err := parseCertAndKey(creds.CertificatePEM, creds.PrivateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("msteams: parse certificate: %w", err)
		}
		ts.certPEM = creds.CertificatePEM
		ts.privKey = privKey
		ts.certThumb = thumb
	} else if creds.ClientSecret == "" {
		return nil, fmt.Errorf("msteams: either client_secret or certificate_pem+private_key_pem is required")
	}

	return ts, nil
}

// Token returns a valid access token for the given scope, refreshing as needed.
func (ts *tokenSource) Token(ctx context.Context, scope string) (string, error) {
	ts.mu.Lock()
	if tok, ok := ts.tokens[scope]; ok && time.Now().Before(tok.expiresAt) {
		ts.mu.Unlock()
		return tok.accessToken, nil
	}
	ts.mu.Unlock()

	return ts.fetch(ctx, scope)
}

func (ts *tokenSource) fetch(ctx context.Context, scope string) (string, error) {
	ts.mu.Lock()
	// Double-check under lock.
	if tok, ok := ts.tokens[scope]; ok && time.Now().Before(tok.expiresAt) {
		ts.mu.Unlock()
		return tok.accessToken, nil
	}

	// Singleflight: if another goroutine is already fetching, wait.
	if ts.inflight {
		ch := ts.inflightCh
		ts.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		ts.mu.Lock()
		tok := ts.tokens[scope]
		ts.mu.Unlock()
		if tok.accessToken != "" {
			return tok.accessToken, nil
		}
		// Fall through and re-fetch (previous fetch may have been for a different scope).
		return ts.fetch(ctx, scope)
	}

	ts.inflight = true
	ts.inflightCh = make(chan struct{})
	ts.mu.Unlock()

	tok, err := ts.doFetch(ctx, scope)

	ts.mu.Lock()
	ts.inflight = false
	close(ts.inflightCh)
	if err == nil {
		ts.tokens[scope] = tok
	}
	ts.mu.Unlock()

	if err != nil {
		return "", err
	}
	return tok.accessToken, nil
}

func (ts *tokenSource) doFetch(ctx context.Context, scope string) (cachedToken, error) {
	endpoint := fmt.Sprintf(tokenEndpointFmt, ts.tenantID)

	params := url.Values{}
	params.Set("grant_type", "client_credentials")
	params.Set("client_id", ts.appID)
	params.Set("scope", scope)

	if ts.privKey != nil {
		assertion, err := ts.buildClientAssertion(endpoint)
		if err != nil {
			return cachedToken{}, fmt.Errorf("build client assertion: %w", err)
		}
		params.Set("client_assertion_type", clientAssertionType)
		params.Set("client_assertion", assertion)
	} else {
		params.Set("client_secret", ts.clientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return cachedToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := ts.httpClient.Do(req)
	if err != nil {
		return cachedToken{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return cachedToken{}, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return cachedToken{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return cachedToken{}, fmt.Errorf("parse token response: %w", err)
	}
	if result.AccessToken == "" {
		return cachedToken{}, fmt.Errorf("empty access_token in response")
	}

	expiry := time.Now().Add(time.Duration(result.ExpiresIn)*time.Second - tokenRefreshMargin)
	return cachedToken{accessToken: result.AccessToken, expiresAt: expiry}, nil
}

// buildClientAssertion builds an RFC 7523 JWT signed with the private key.
// The header includes x5t#S256 (SHA-256 thumbprint of the certificate).
func (ts *tokenSource) buildClientAssertion(audience string) (string, error) {
	now := time.Now()
	header := map[string]any{
		"alg":     "RS256",
		"typ":     "JWT",
		"x5t#S256": ts.certThumb,
	}
	payload := map[string]any{
		"iss": ts.appID,
		"sub": ts.appID,
		"aud": audience,
		"jti": fmt.Sprintf("%d", now.UnixNano()),
		"nbf": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"iat": now.Unix(),
	}

	hdr, err := base64urlJSON(header)
	if err != nil {
		return "", err
	}
	pay, err := base64urlJSON(payload)
	if err != nil {
		return "", err
	}
	signing := hdr + "." + pay
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, ts.privKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign assertion: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func base64urlJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parseCertAndKey parses a PEM certificate and private key, returning the RSA key
// and the base64url(SHA-256(DER)) thumbprint of the certificate.
func parseCertAndKey(certPEM, keyPEM string) (*rsa.PrivateKey, string, error) {
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil {
		return nil, "", fmt.Errorf("no PEM block found in certificate")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse certificate: %w", err)
	}
	thumbSum := sha256.Sum256(cert.Raw)
	thumb := base64.RawURLEncoding.EncodeToString(thumbSum[:])

	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, "", fmt.Errorf("no PEM block found in private key")
	}

	var privKey *rsa.PrivateKey
	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		privKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "PRIVATE KEY":
		ki, e := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if e != nil {
			return nil, "", fmt.Errorf("parse PKCS8 key: %w", e)
		}
		var ok bool
		privKey, ok = ki.(*rsa.PrivateKey)
		if !ok {
			return nil, "", fmt.Errorf("private key is not RSA")
		}
	default:
		return nil, "", fmt.Errorf("unsupported PEM key type: %s", keyBlock.Type)
	}
	if err != nil {
		return nil, "", fmt.Errorf("parse private key: %w", err)
	}
	_ = crypto.SHA256 // ensure crypto/sha256 is linked

	return privKey, thumb, nil
}
