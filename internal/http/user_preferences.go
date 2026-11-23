package http

import (
	"net/http"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// UserPreferencesHandler handles per-user preferences endpoints.
type UserPreferencesHandler struct {
	tenantStore store.TenantStore
	agentStore  store.AgentStore
}

// NewUserPreferencesHandler creates a handler for user preferences endpoints.
func NewUserPreferencesHandler(ts store.TenantStore, as store.AgentStore) *UserPreferencesHandler {
	return &UserPreferencesHandler{tenantStore: ts, agentStore: as}
}

// RegisterRoutes registers user preferences routes.
func (h *UserPreferencesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/me/preferences", h.auth(h.handleGet))
	mux.HandleFunc("PATCH /v1/me/preferences", h.auth(h.handlePatch))
}

func (h *UserPreferencesHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth("", next)
}

// handleGet returns the caller's preferences.
func (h *UserPreferencesHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	locale := store.LocaleFromContext(r.Context())
	userID := extractUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgUnauthorized))
		return
	}
	tenantID := store.TenantIDFromContext(r.Context())
	prefs, err := h.tenantStore.GetUserPreferences(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgInternalError, err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// handlePatch merges updates into the caller's preferences.
func (h *UserPreferencesHandler) handlePatch(w http.ResponseWriter, r *http.Request) {
	locale := store.LocaleFromContext(r.Context())
	userID := extractUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgUnauthorized))
		return
	}
	tenantID := store.TenantIDFromContext(r.Context())

	var input store.UserPreferences
	if !bindJSON(w, r, locale, &input) {
		return
	}

	// The agent must exist in the caller's tenant (GetByID is tenant-scoped).
	if input.DefaultAgentID != nil {
		if _, err := h.agentStore.GetByID(r.Context(), *input.DefaultAgentID); err != nil {
			writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgAgentNotFound, input.DefaultAgentID.String()))
			return
		}
	}

	// Get existing prefs, merge with input
	existing, err := h.tenantStore.GetUserPreferences(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgInternalError, err.Error()))
		return
	}

	// Merge: input takes precedence
	if input.DefaultAgentID != nil {
		existing.DefaultAgentID = input.DefaultAgentID
	}

	if err := h.tenantStore.UpdateUserPreferences(r.Context(), tenantID, userID, existing); err != nil {
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgInternalError, err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, existing)
}
