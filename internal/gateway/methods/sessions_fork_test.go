package methods

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

func asst(id string, callIDs ...string) providers.Message {
	m := providers.Message{ID: id, Role: "assistant", Content: "a-" + id}
	for _, c := range callIDs {
		m.ToolCalls = append(m.ToolCalls, providers.ToolCall{ID: c, Name: "t"})
	}
	return m
}

func tool(id, callID string) providers.Message {
	return providers.Message{ID: id, Role: "tool", ToolCallID: callID, Content: "r"}
}

func user(id string) providers.Message {
	return providers.Message{ID: id, Role: "user", Content: "u-" + id}
}

func TestSnapForkEnd(t *testing.T) {
	// 0 user, 1 assistant(call c1,c2), 2 tool c1, 3 tool c2, 4 assistant final, 5 user, 6 assistant(call c3) — c3 unanswered
	h := []providers.Message{
		user("u1"), asst("a1", "c1", "c2"), tool("t1", "c1"), tool("t2", "c2"), asst("a2"), user("u2"), asst("a3", "c3"),
	}
	tests := []struct {
		name string
		end  int
		want int
	}{
		{"plain user turn", 1, 1},
		{"cut right after assistant with tool_calls pulls in all results", 2, 4},
		{"cut between tool results pulls in the rest", 3, 4},
		{"cut after last tool result is unchanged", 4, 4},
		{"cut after final assistant is unchanged", 5, 5},
		{"trailing assistant with unanswered tool_calls is dropped", 7, 6},
		{"zero stays zero", 0, 0},
		{"beyond length is clamped", 99, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapForkEnd(h, tt.end); got != tt.want {
				t.Fatalf("snapForkEnd(end=%d) = %d, want %d", tt.end, got, tt.want)
			}
		})
	}
}

// ---- handler tests ----

type forkStubStore struct {
	*stubSessionStore
	history []providers.Message
	gotOpts store.SessionBranchOpts
	gotSrc  string
	called  bool
	err     error
}

func (s *forkStubStore) GetHistory(_ context.Context, _ string) []providers.Message {
	return s.history
}

func (s *forkStubStore) BranchSession(_ context.Context, src string, opts store.SessionBranchOpts) (*store.SessionData, int, error) {
	s.called, s.gotSrc, s.gotOpts = true, src, opts
	if s.err != nil {
		return nil, 0, s.err
	}
	return &store.SessionData{Key: opts.NewKey}, opts.UpToIndex, nil
}

type forkStubAgents struct {
	store.AgentStore
	agents map[string]*store.AgentData
}

func (a *forkStubAgents) GetByKey(_ context.Context, key string) (*store.AgentData, error) {
	if d, ok := a.agents[key]; ok {
		return d, nil
	}
	return nil, context.Canceled // any non-nil error = not found
}

func runFork(t *testing.T, m *SessionsMethods, role permissions.Role, userID string, params map[string]any) protocol.ResponseFrame {
	t.Helper()
	client, ch := gateway.NewCapturingTestClient(role, uuid.Nil, userID, 4)
	m.handleFork(context.Background(), client, sessionReqFrame(t, protocol.MethodSessionsFork, params))
	select {
	case raw := <-ch:
		var resp protocol.ResponseFrame
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return resp
	default:
		t.Fatal("handler sent no response")
		return protocol.ResponseFrame{}
	}
}

func newForkFixture(t *testing.T) (*SessionsMethods, *forkStubStore, uuid.UUID) {
	base := newStubSessionStore()
	base.addSession("agent:writer:ws:direct:src", "alice")
	st := &forkStubStore{
		stubSessionStore: base,
		history:          []providers.Message{user("u1"), asst("a1"), user("u2"), asst("a2")},
	}
	targetID := uuid.New()
	m := buildSessionMethods(t, base)
	m.sessions = st
	m.SetAgentStore(&forkStubAgents{agents: map[string]*store.AgentData{
		"coder": {BaseModel: store.BaseModel{ID: targetID}, AgentKey: "coder", Status: store.AgentStatusActive, Provider: "anthropic", Model: "claude-x", ContextWindow: 123456},
	}})
	return m, st, targetID
}

func TestHandleFork_CopiesUpToMessageID_AndBindsTargetAgent(t *testing.T) {
	m, st, targetID := newForkFixture(t)
	resp := runFork(t, m, permissions.RoleOperator, "alice", map[string]any{
		"sourceKey": "agent:writer:ws:direct:src", "newKey": "agent:coder:ws:direct:new", "upToMessageId": "a1",
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error %+v", resp.Error)
	}
	if !st.called || st.gotSrc != "agent:writer:ws:direct:src" {
		t.Fatalf("BranchSession not called with source key: called=%v src=%q", st.called, st.gotSrc)
	}
	o := st.gotOpts
	if o.UpToIndex != 2 { // user u1 + assistant a1
		t.Errorf("UpToIndex = %d, want 2 (resolved from message id, not position)", o.UpToIndex)
	}
	if o.AgentUUID != targetID || o.Model != "claude-x" || o.Provider != "anthropic" || o.ContextWindow != 123456 {
		t.Errorf("target agent binding wrong: %+v", o)
	}
	if o.UserID != "alice" {
		t.Errorf("branch owner = %q, want caller alice", o.UserID)
	}
}

func TestHandleFork_UnknownMessageID_NotFound(t *testing.T) {
	m, st, _ := newForkFixture(t)
	resp := runFork(t, m, permissions.RoleOperator, "alice", map[string]any{
		"sourceKey": "agent:writer:ws:direct:src", "newKey": "agent:coder:ws:direct:new", "upToMessageId": "nope",
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrNotFound {
		t.Fatalf("want NOT_FOUND, got %+v", resp)
	}
	if st.called {
		t.Fatal("BranchSession must not be called for an unknown message id")
	}
}

func TestHandleFork_UnknownOrInactiveTargetAgent_NotFound(t *testing.T) {
	m, st, _ := newForkFixture(t)
	resp := runFork(t, m, permissions.RoleOperator, "alice", map[string]any{
		"sourceKey": "agent:writer:ws:direct:src", "newKey": "agent:ghost:ws:direct:new", "upToMessageId": "a1",
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrNotFound {
		t.Fatalf("want NOT_FOUND, got %+v", resp)
	}
	if st.called {
		t.Fatal("BranchSession must not be called when the target agent does not exist")
	}
}

func TestHandleFork_ExistingKey_IsError(t *testing.T) {
	m, st, _ := newForkFixture(t)
	st.err = store.ErrSessionAlreadyExists
	resp := runFork(t, m, permissions.RoleOperator, "alice", map[string]any{
		"sourceKey": "agent:writer:ws:direct:src", "newKey": "agent:coder:ws:direct:new", "upToMessageId": "a1",
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrFailedPrecondition {
		t.Fatalf("want FAILED_PRECONDITION, got %+v", resp)
	}
}

func TestHandleFork_NonOwnerDenied(t *testing.T) {
	m, st, _ := newForkFixture(t)
	resp := runFork(t, m, permissions.RoleOperator, "mallory", map[string]any{
		"sourceKey": "agent:writer:ws:direct:src", "newKey": "agent:coder:ws:direct:new", "upToMessageId": "a1",
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrUnauthorized {
		t.Fatalf("want UNAUTHORIZED, got %+v", resp)
	}
	if st.called {
		t.Fatal("BranchSession must not run for a non-owner")
	}
}

func TestHandleFork_MissingMessageID_InvalidRequest(t *testing.T) {
	m, _, _ := newForkFixture(t)
	resp := runFork(t, m, permissions.RoleOperator, "alice", map[string]any{
		"sourceKey": "agent:writer:ws:direct:src", "newKey": "agent:coder:ws:direct:new",
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrInvalidRequest {
		t.Fatalf("want INVALID_REQUEST, got %+v", resp)
	}
}
