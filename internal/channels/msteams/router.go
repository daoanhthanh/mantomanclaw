package msteams

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
)

const (
	webhookPath   = "/v1/channels/msteams/messages"
	maxBodySize   = 1 << 20 // 1 MiB
)

// webhookRouter is the process-wide shared HTTP handler for all msteams channel instances.
// Routes inbound activities to the correct channel by JWT audience (bot App ID).
type webhookRouter struct {
	mu           sync.RWMutex
	instances    map[string]*Channel // appID → channel
	routeClaimed bool
}

var globalRouter = &webhookRouter{
	instances: make(map[string]*Channel),
}

func (r *webhookRouter) register(ch *Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[ch.creds.AppID] = ch
}

func (r *webhookRouter) unregister(appID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.instances, appID)
}

// ClaimWebhookRoute returns the path and handler on the first call; ("", nil) thereafter.
// Idempotent: safe to call unconditionally from gateway lifecycle (mirrors Bitrix24).
func (r *webhookRouter) ClaimWebhookRoute() (string, http.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.routeClaimed {
		r.routeClaimed = true
		return webhookPath, r
	}
	return "", nil
}

// WebhookRouter returns the process-wide shared router.
func WebhookRouter() *webhookRouter {
	return globalRouter
}

// resetWebhookRouterForTest resets the singleton for unit tests.
func resetWebhookRouterForTest() {
	globalRouter.mu.Lock()
	defer globalRouter.mu.Unlock()
	globalRouter.instances = make(map[string]*Channel)
	globalRouter.routeClaimed = false
}

// ServeHTTP is the shared handler for all msteams activities.
// It:
//  1. Reads the body (capped at 1 MiB) and returns 200 immediately.
//  2. Verifies the JWT using the audience claim (bot App ID) to route to the right channel.
//  3. Dispatches asynchronously so Teams gets its 200 within the 15-second deadline.
func (r *webhookRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Read body first (before JWT check) only to get serviceUrl for the JWT check.
	// If body exceeds limit, reject before JSON decode.
	if req.ContentLength > maxBodySize {
		slog.Warn("security.msteams.body_too_large", "content_length", req.ContentLength)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodySize+1))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxBodySize {
		slog.Warn("security.msteams.body_too_large")
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Minimal decode to get serviceUrl before JWT verification.
	// serviceUrl is a required field; fail fast if missing.
	var partial struct {
		ServiceURL string `json:"serviceUrl"`
	}
	if err := json.Unmarshal(body, &partial); err != nil {
		slog.Warn("security.msteams.malformed_body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	authHeader := req.Header.Get("Authorization")

	// Find a channel that can verify this JWT (we try all registered instances;
	// the JWT audience uniquely identifies the right one).
	r.mu.RLock()
	instances := make([]*Channel, 0, len(r.instances))
	for _, ch := range r.instances {
		instances = append(instances, ch)
	}
	r.mu.RUnlock()

	if len(instances) == 0 {
		// No channels registered yet; return 200 to avoid Teams retry storms.
		w.WriteHeader(http.StatusOK)
		return
	}

	var target *Channel
	for _, ch := range instances {
		if err := ch.verifier.Verify(req.Context(), authHeader, partial.ServiceURL); err == nil {
			target = ch
			break
		}
	}

	if target == nil {
		slog.Warn("security.msteams.jwt_rejected",
			"remote_addr", req.RemoteAddr,
			"service_url", partial.ServiceURL,
			"reason", "no channel accepted the JWT")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Ack before processing — Teams retries on slow 200s.
	w.WriteHeader(http.StatusOK)

	// Dispatch asynchronously.
	bodyCopy := make([]byte, len(body))
	copy(bodyCopy, body)
	go target.handleActivity(req.Context(), bodyCopy)
}
