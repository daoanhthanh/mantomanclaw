package msteams

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	botFrameworkOIDCURL = "https://login.botframework.com/v1/.well-known/openidconfiguration"
	botFrameworkIssuer  = "https://api.botframework.com"

	jwksCacheTTL         = 24 * time.Hour
	jwksForceRefreshRate = 5 * time.Minute // min interval between unknown-kid refreshes
)

// jwksCache caches the Bot Framework JWKS keys.
type jwksCache struct {
	mu           sync.RWMutex
	keys         map[string]*rsa.PublicKey // kid → key
	fetchedAt    time.Time
	lastForceAt  time.Time
	jwksURI      string
	httpClient   *http.Client
}

func newJWKSCache() *jwksCache {
	return &jwksCache{
		keys:       make(map[string]*rsa.PublicKey),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// key returns the RSA public key for kid, refreshing the cache when needed.
func (c *jwksCache) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	k, ok := c.keys[kid]
	stale := time.Since(c.fetchedAt) > jwksCacheTTL
	c.mu.RUnlock()

	if ok && !stale {
		return k, nil
	}

	// Need to refresh. If kid is unknown and we refreshed recently, rate-limit.
	if !ok {
		c.mu.RLock()
		tooSoon := time.Since(c.lastForceAt) < jwksForceRefreshRate
		c.mu.RUnlock()
		if tooSoon {
			return nil, fmt.Errorf("msteams: unknown signing key kid=%q; refresh rate-limited", kid)
		}
	}

	if err := c.refresh(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	k, ok = c.keys[kid]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("msteams: signing key kid=%q not found in JWKS after refresh", kid)
	}
	return k, nil
}

// refresh fetches the OpenID configuration, then the JWKS.
func (c *jwksCache) refresh(ctx context.Context) error {
	// Resolve jwks_uri from OIDC metadata if we don't have it yet.
	c.mu.RLock()
	uri := c.jwksURI
	c.mu.RUnlock()

	if uri == "" {
		var meta struct {
			JWKSURI string `json:"jwks_uri"`
		}
		if err := c.fetchJSON(ctx, botFrameworkOIDCURL, &meta); err != nil {
			return fmt.Errorf("msteams: fetch OIDC config: %w", err)
		}
		if meta.JWKSURI == "" {
			return fmt.Errorf("msteams: OIDC config missing jwks_uri")
		}
		uri = meta.JWKSURI
	}

	var jwks struct {
		Keys []struct {
			Kid string   `json:"kid"`
			N   string   `json:"n"`
			E   string   `json:"e"`
			Kty string   `json:"kty"`
			X5c []string `json:"x5c"`
		} `json:"keys"`
	}
	if err := c.fetchJSON(ctx, uri, &jwks); err != nil {
		return fmt.Errorf("msteams: fetch JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" {
			continue
		}
		pub, err := rsaKeyFromJWK(k.N, k.E)
		if err != nil {
			slog.Warn("msteams: skip malformed JWKS key", "kid", k.Kid, "error", err)
			continue
		}
		keys[k.Kid] = pub
	}

	c.mu.Lock()
	c.jwksURI = uri
	c.keys = keys
	c.fetchedAt = time.Now()
	c.lastForceAt = time.Now()
	c.mu.Unlock()
	return nil
}

func (c *jwksCache) fetchJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func rsaKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	eInt := 0
	for _, b := range eBytes {
		eInt = (eInt << 8) | int(b)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: eInt,
	}, nil
}

// jwtVerifier verifies Bot Framework JWTs.
type jwtVerifier struct {
	appID string
	cache *jwksCache
}

func newJWTVerifier(appID string) *jwtVerifier {
	return &jwtVerifier{appID: appID, cache: newJWKSCache()}
}

// botFrameworkClaims extends RegisteredClaims with Bot Framework extras.
type botFrameworkClaims struct {
	jwt.RegisteredClaims
	ServiceURL string `json:"serviceurl"`
}

// Verify parses and validates the Authorization header value.
// activityServiceURL is the serviceUrl field from the decoded activity body
// (must be provided AFTER the JWT is verified to avoid unauthenticated use).
func (v *jwtVerifier) Verify(ctx context.Context, authHeader, activityServiceURL string) error {
	bearer := strings.TrimPrefix(authHeader, "Bearer ")
	bearer = strings.TrimPrefix(bearer, "bearer ")
	if bearer == "" || bearer == authHeader {
		return fmt.Errorf("missing or malformed Authorization header")
	}

	// Parse header to get kid before full verification.
	unverified, _, err := new(jwt.Parser).ParseUnverified(bearer, &botFrameworkClaims{})
	if err != nil {
		return fmt.Errorf("parse JWT header: %w", err)
	}
	kid, _ := unverified.Method.(*jwt.SigningMethodRSA) // may be nil if alg is wrong
	_ = kid
	kidStr, _ := unverified.Header["kid"].(string)

	pub, err := v.cache.key(ctx, kidStr)
	if err != nil {
		return fmt.Errorf("resolve signing key: %w", err)
	}

	var claims botFrameworkClaims
	token, err := jwt.ParseWithClaims(bearer, &claims,
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pub, nil
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(botFrameworkIssuer),
		jwt.WithAudience(v.appID),
		jwt.WithLeeway(5*time.Minute),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return fmt.Errorf("JWT validation: %w", err)
	}
	if !token.Valid {
		return fmt.Errorf("JWT not valid")
	}

	// serviceUrl claim must match activity.serviceUrl (case-insensitive, strip trailing slash).
	claimedURL := strings.TrimRight(strings.ToLower(claims.ServiceURL), "/")
	actURL := strings.TrimRight(strings.ToLower(activityServiceURL), "/")
	if claimedURL != "" && actURL != "" && claimedURL != actURL {
		return fmt.Errorf("serviceUrl mismatch: JWT=%q activity=%q", claims.ServiceURL, activityServiceURL)
	}

	return nil
}
