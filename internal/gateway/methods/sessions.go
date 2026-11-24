package methods

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	httpapi "github.com/nextlevelbuilder/goclaw/internal/http"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// SessionsMethods handles sessions.list, sessions.preview, sessions.patch, sessions.delete, sessions.reset, sessions.compact, sessions.fork.
type SessionsMethods struct {
	sessions   store.SessionStore
	agentStore store.AgentStore // optional: resolves the target agent for sessions.fork
	eventBus   bus.EventPublisher
	cfg        *config.Config
}

func NewSessionsMethods(sess store.SessionStore, eventBus bus.EventPublisher, cfg *config.Config) *SessionsMethods {
	return &SessionsMethods{sessions: sess, eventBus: eventBus, cfg: cfg}
}

// SetAgentStore wires the agent store used by sessions.fork to bind a forked
// session to its target agent.
func (m *SessionsMethods) SetAgentStore(as store.AgentStore) {
	m.agentStore = as
}

func (m *SessionsMethods) Register(router *gateway.MethodRouter) {
	router.Register(protocol.MethodSessionsList, m.handleList)
	router.Register(protocol.MethodSessionsPreview, m.handlePreview)
	router.Register(protocol.MethodSessionsPatch, m.handlePatch)
	router.Register(protocol.MethodSessionsDelete, m.handleDelete)
	router.Register(protocol.MethodSessionsReset, m.handleReset)
	router.Register(protocol.MethodSessionsCompact, m.handleCompact)
	router.Register(protocol.MethodSessionsFork, m.handleFork)
}

type sessionsListParams struct {
	AgentID string `json:"agentId"`
	Channel string `json:"channel"` // optional: filter by channel prefix ("ws", "telegram")
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

func (m *SessionsMethods) handleList(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	var params sessionsListParams
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	if params.Limit <= 0 {
		params.Limit = 20
	}

	opts := store.SessionListOpts{
		AgentID:  params.AgentID,
		Channel:  params.Channel,
		Limit:    params.Limit,
		Offset:   params.Offset,
		TenantID: store.TenantIDFromContext(ctx),
	}
	// Role-based filtering: admins/owners see all sessions; regular users see only their own.
	// Tenant scope is always applied above — admin sees all sessions within the tenant.
	if !canSeeAll(client.Role(), m.cfg.Gateway.OwnerIDs, client.UserID()) {
		opts.UserID = client.UserID()
	}

	result := m.sessions.ListPagedRich(ctx, opts)
	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"sessions": result.Sessions,
		"total":    result.Total,
		"limit":    params.Limit,
		"offset":   params.Offset,
	}))
}

type sessionKeyParams struct {
	Key string `json:"key"`
}

func (m *SessionsMethods) handlePreview(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params sessionKeyParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON)))
		return
	}

	if !canSeeAll(client.Role(), m.cfg.Gateway.OwnerIDs, client.UserID()) {
		sess := m.sessions.Get(ctx, params.Key)
		if sess == nil {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "session", params.Key)))
			return
		}
		if sess.UserID != client.UserID() {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgPermissionDenied, "session")))
			return
		}
	}

	history := m.sessions.GetHistory(ctx, params.Key)
	summary := m.sessions.GetSummary(ctx, params.Key)

	// Sign file URLs before delivery — sessions store clean paths.
	secret := httpapi.FileSigningKey()
	for i := range history {
		history[i].Content = httpapi.SignFileURLs(history[i].Content, secret)
		for j := range history[i].MediaRefs {
			history[i].MediaRefs[j].Path = httpapi.SignMediaPath(history[i].MediaRefs[j].Path, secret)
		}
	}
	summary = httpapi.SignFileURLs(summary, secret)

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"key":      params.Key,
		"messages": history,
		"summary":  summary,
	}))
}

// handlePatch updates session metadata fields.
// Matching TS sessions.patch (src/gateway/server-methods/sessions.ts:237-287).
func (m *SessionsMethods) handlePatch(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params struct {
		Key      string            `json:"key"`
		Label    *string           `json:"label,omitempty"`
		Model    *string           `json:"model,omitempty"`
		Metadata map[string]string `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON)))
		return
	}

	if params.Key == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "key")))
		return
	}

	if !canSeeAll(client.Role(), m.cfg.Gateway.OwnerIDs, client.UserID()) {
		sess := m.sessions.Get(ctx, params.Key)
		if sess == nil {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "session", params.Key)))
			return
		}
		if sess.UserID != client.UserID() {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgPermissionDenied, "session")))
			return
		}
	}

	// Apply label patch
	if params.Label != nil {
		m.sessions.SetLabel(ctx, params.Key, *params.Label)
	}

	// Apply model patch
	if params.Model != nil {
		m.sessions.UpdateMetadata(ctx, params.Key, *params.Model, "", "")
	}

	// Apply metadata patch
	if len(params.Metadata) > 0 {
		m.sessions.SetSessionMetadata(ctx, params.Key, params.Metadata)
	}

	// Save changes to DB
	m.sessions.Save(ctx, params.Key)

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok":  true,
		"key": params.Key,
	}))
	emitAudit(m.eventBus, client, "session.patched", "session", params.Key)
}

func (m *SessionsMethods) handleDelete(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params sessionKeyParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON)))
		return
	}

	if !canSeeAll(client.Role(), m.cfg.Gateway.OwnerIDs, client.UserID()) {
		sess := m.sessions.Get(ctx, params.Key)
		if sess == nil {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "session", params.Key)))
			return
		}
		if sess.UserID != client.UserID() {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgPermissionDenied, "session")))
			return
		}
	}

	if err := m.sessions.Delete(ctx, params.Key); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, err.Error()))
		return
	}

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok": true,
	}))
	emitAudit(m.eventBus, client, "session.deleted", "session", params.Key)
}

// cliSessionReset clears the Claude CLI-backed session for a key. It is a
// package var so tests can stub it; defaults to the real provider helper.
var cliSessionReset = providers.ResetCLISession

func (m *SessionsMethods) handleReset(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params sessionKeyParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON)))
		return
	}

	if !canSeeAll(client.Role(), m.cfg.Gateway.OwnerIDs, client.UserID()) {
		sess := m.sessions.Get(ctx, params.Key)
		if sess == nil {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "session", params.Key)))
			return
		}
		if sess.UserID != client.UserID() {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgPermissionDenied, "session")))
			return
		}
	}

	m.sessions.Reset(ctx, params.Key)
	// Also clear the Claude CLI session (.jsonl + CLAUDE.md) so the RPC reset
	// matches the /reset chat command for claude-cli-backed agents (no-op when
	// the CLI provider is unused).
	cliSessionReset("", params.Key)

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok": true,
	}))
	emitAudit(m.eventBus, client, "session.reset", "session", params.Key)
}

type sessionCompactParams struct {
	Key      string `json:"key"`
	KeepLast int    `json:"keepLast,omitempty"` // default 4
}

// handleCompact truncates session history to the last N messages.
// Issue 958: Manual session compaction API (truncate-only, no LLM summarization).
func (m *SessionsMethods) handleCompact(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params sessionCompactParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON)))
		return
	}

	if params.Key == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, "key is required"))
		return
	}

	keepLast := params.KeepLast
	if keepLast <= 0 {
		keepLast = 4 // default: keep last 2 exchanges
	}

	// Auth check
	sess := m.sessions.Get(ctx, params.Key)
	if sess == nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "session", params.Key)))
		return
	}
	if !canSeeAll(client.Role(), m.cfg.Gateway.OwnerIDs, client.UserID()) {
		if sess.UserID != client.UserID() {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgPermissionDenied, "session")))
			return
		}
	}

	history := m.sessions.GetHistory(ctx, params.Key)
	originalLen := len(history)
	if originalLen < 6 {
		client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
			"ok":      true,
			"message": "session too short to compact",
			"kept":    originalLen,
		}))
		return
	}

	// Truncate history to last N messages
	m.sessions.TruncateHistory(ctx, params.Key, keepLast)
	m.sessions.IncrementCompaction(ctx, params.Key)
	m.sessions.Save(ctx, params.Key)

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok":       true,
		"original": originalLen,
		"kept":     keepLast,
	}))
	emitAudit(m.eventBus, client, "session.compacted", "session", params.Key)
}

type sessionForkParams struct {
	SourceKey string `json:"sourceKey"`
	NewKey    string `json:"newKey"`
	// UpToMessageID is the ID of the last message to copy (inclusive).
	UpToMessageID string `json:"upToMessageId"`
	Label         string `json:"label"`
}

// snapForkEnd takes the exclusive end index of a fork and moves it so the copied
// history stays valid for LLM providers: an assistant turn with tool_calls is
// either copied together with all of its tool results, or dropped entirely.
func snapForkEnd(history []providers.Message, end int) int {
	if end > len(history) {
		end = len(history)
	}
	if end <= 0 {
		return 0
	}
	// Pull in tool results that follow the cut and belong to the same turn.
	for end < len(history) && history[end].Role == "tool" &&
		(history[end-1].Role == "tool" || (history[end-1].Role == "assistant" && len(history[end-1].ToolCalls) > 0)) {
		end++
	}
	// Find the assistant turn that owns the tail of the range.
	owner := end - 1
	for owner >= 0 && history[owner].Role == "tool" {
		owner--
	}
	if owner < 0 || history[owner].Role != "assistant" || len(history[owner].ToolCalls) == 0 {
		return end
	}
	answered := make(map[string]bool, end-owner)
	for _, m := range history[owner+1 : end] {
		answered[m.ToolCallID] = true
	}
	for _, tc := range history[owner].ToolCalls {
		if !answered[tc.ID] {
			return owner // incomplete turn: drop it rather than send dangling tool_calls
		}
	}
	return end
}

// handleFork branches a session into a new key (possibly under a different agent),
// copying history up to and including the message identified by upToMessageId.
// The branch is owned by the caller and bound to the agent named in newKey.
func (m *SessionsMethods) handleFork(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params sessionForkParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON)))
		return
	}
	switch {
	case params.SourceKey == "":
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "sourceKey")))
		return
	case params.NewKey == "":
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "newKey")))
		return
	case params.UpToMessageID == "":
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "upToMessageId")))
		return
	}
	targetKey, _ := sessions.ParseSessionKey(params.NewKey)
	if targetKey == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "invalid newKey")))
		return
	}

	// Ownership check on source session.
	if !requireSessionOwner(ctx, m.sessions, m.cfg, client, req.ID, params.SourceKey) {
		return
	}

	brancher, ok := m.sessions.(store.SessionBranchStore)
	if !ok {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, i18n.T(locale, i18n.MsgInvalidRequest, "session branching unavailable")))
		return
	}

	// Resolve the cut point from the message ID, not a client-side position.
	history := m.sessions.GetHistory(ctx, params.SourceKey)
	end := -1
	for i := range history {
		if history[i].ID == params.UpToMessageID {
			end = i + 1
			break
		}
	}
	if end < 0 {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "message", params.UpToMessageID)))
		return
	}
	end = snapForkEnd(history, end)
	if end == 0 {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "nothing to fork")))
		return
	}

	opts := store.SessionBranchOpts{
		NewKey:    params.NewKey,
		UpToIndex: end,
		Label:     params.Label,
		UserID:    client.UserID(),
	}
	// Bind the branch to the target agent so listings, usage and the agent loop
	// attribute it correctly instead of inheriting the source agent's identity.
	if m.agentStore != nil {
		target, err := m.agentStore.GetByKey(ctx, targetKey)
		if err != nil || target == nil || target.Status != store.AgentStatusActive {
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "agent", targetKey)))
			return
		}
		opts.AgentUUID = target.ID
		opts.Model = target.Model
		opts.Provider = target.Provider
		opts.ContextWindow = target.ContextWindow
	}

	branch, copied, err := brancher.BranchSession(ctx, params.SourceKey, opts)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrSessionAlreadyExists):
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrFailedPrecondition, i18n.T(locale, i18n.MsgAlreadyExists, "session", params.NewKey)))
		case errors.Is(err, store.ErrInvalidSessionBranch):
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "fork point out of range")))
		case errors.Is(err, store.ErrSessionNotFound):
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "session", params.SourceKey)))
		default:
			client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, err.Error()))
		}
		return
	}

	emitAudit(m.eventBus, client, "session.forked", "session", branch.Key)
	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok":             true,
		"sessionKey":     branch.Key,
		"copiedMessages": copied,
	}))
}
