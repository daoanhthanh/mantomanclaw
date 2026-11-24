//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"errors"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestBranchSessionPersistsCopyAndMetadata(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	sessionStore := NewSQLiteSessionStore(db)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	sourceKey := "agent:test-agent:direct:source-branch-test"
	branchKey := "agent:test-agent:branch:copy-branch-test"

	source := sessionStore.GetOrCreate(ctx, sourceKey)
	source.Summary = "source summary"
	source.Metadata = map[string]string{"source": "yes"}
	sessionStore.SetHistory(ctx, sourceKey, []providers.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
	})
	if err := sessionStore.Save(ctx, sourceKey); err != nil {
		t.Fatalf("Save source: %v", err)
	}

	branch, copied, err := sessionStore.BranchSession(ctx, sourceKey, store.SessionBranchOpts{
		NewKey:    branchKey,
		UpToIndex: 1,
		Label:     "branch label",
		Metadata:  map[string]string{"requested_by": "test"},
	})
	if err != nil {
		t.Fatalf("BranchSession: %v", err)
	}
	if copied != 1 {
		t.Fatalf("copied = %d, want 1", copied)
	}
	if branch.Key != branchKey || branch.Channel != "branch" || branch.Label != "branch label" {
		t.Fatalf("branch metadata = key:%q channel:%q label:%q", branch.Key, branch.Channel, branch.Label)
	}
	if len(branch.Messages) != 1 || branch.Messages[0].Content != "first" {
		t.Fatalf("branch messages = %+v", branch.Messages)
	}
	if branch.Metadata["branched_from"] != sourceKey || branch.Metadata["branched_from_index"] != "1" {
		t.Fatalf("branch provenance metadata = %+v", branch.Metadata)
	}
	if branch.Metadata["source"] != "yes" || branch.Metadata["requested_by"] != "test" {
		t.Fatalf("branch merged metadata = %+v", branch.Metadata)
	}

	reloaded := NewSQLiteSessionStore(db).Get(ctx, branchKey)
	if reloaded == nil {
		t.Fatal("reloaded branch = nil")
	}
	if len(reloaded.Messages) != 1 || reloaded.Messages[0].Content != "first" {
		t.Fatalf("reloaded messages = %+v", reloaded.Messages)
	}
	if reloaded.Metadata["branched_from"] != sourceKey {
		t.Fatalf("reloaded metadata = %+v", reloaded.Metadata)
	}
}

func TestBranchSessionRejectsExistingTargetWithoutOverwrite(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	sourceKey := "agent:test-agent:direct:source-conflict-test"
	branchKey := "agent:test-agent:branch:conflict-test"

	firstStore := NewSQLiteSessionStore(db)
	firstStore.GetOrCreate(ctx, sourceKey)
	firstStore.SetHistory(ctx, sourceKey, []providers.Message{{Role: "user", Content: "first"}})
	if err := firstStore.Save(ctx, sourceKey); err != nil {
		t.Fatalf("Save source: %v", err)
	}
	if _, _, err := firstStore.BranchSession(ctx, sourceKey, store.SessionBranchOpts{
		NewKey:    branchKey,
		UpToIndex: 1,
		Label:     "first label",
	}); err != nil {
		t.Fatalf("BranchSession first: %v", err)
	}

	secondStore := NewSQLiteSessionStore(db)
	_, _, err := secondStore.BranchSession(ctx, sourceKey, store.SessionBranchOpts{
		NewKey:    branchKey,
		UpToIndex: 1,
		Label:     "second label",
	})
	if !errors.Is(err, store.ErrSessionAlreadyExists) {
		t.Fatalf("second BranchSession err = %v, want ErrSessionAlreadyExists", err)
	}

	reloaded := NewSQLiteSessionStore(db).Get(ctx, branchKey)
	if reloaded == nil {
		t.Fatal("reloaded branch = nil")
	}
	if reloaded.Label != "first label" {
		t.Fatalf("label = %q, want first label", reloaded.Label)
	}
}

func TestAddMessageStampsStableID(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	s := NewSQLiteSessionStore(db)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	key := "agent:test-agent:direct:id-stamp"

	s.AddMessage(ctx, key, providers.Message{Role: "user", Content: "a"})
	s.AddMessage(ctx, key, providers.Message{Role: "assistant", Content: "b", ID: "preset"})

	h := s.GetHistory(ctx, key)
	if len(h) != 2 {
		t.Fatalf("history len = %d, want 2", len(h))
	}
	if h[0].ID == "" {
		t.Fatal("AddMessage must assign an ID when empty")
	}
	if h[1].ID != "preset" {
		t.Fatalf("AddMessage must keep a preset ID, got %q", h[1].ID)
	}
	if h[0].ID == h[1].ID {
		t.Fatal("message IDs must be unique")
	}
}

func TestBranchSessionCrossAgentRebindsIdentityAndOwner(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	targetID := seedSQLiteLinkAgent(t, db, store.MasterTenantID, "coder")

	s := NewSQLiteSessionStore(db)
	sourceKey := "agent:writer:ws:direct:src-cross"
	src := s.GetOrCreate(ctx, sourceKey)
	src.UserID = "alice"
	src.Summary = "writer-specific summary"
	src.Model, src.Provider, src.ContextWindow = "writer-model", "writer-provider", 1000
	s.AddMessage(ctx, sourceKey, providers.Message{Role: "user", Content: "hi"})
	s.AddMessage(ctx, sourceKey, providers.Message{Role: "assistant", Content: "yo"})
	if err := s.Save(ctx, sourceKey); err != nil {
		t.Fatalf("Save source: %v", err)
	}
	srcIDs := s.GetHistory(ctx, sourceKey)

	newKey := "agent:coder:ws:direct:dst-cross"
	branch, copied, err := s.BranchSession(ctx, sourceKey, store.SessionBranchOpts{
		NewKey: newKey, UpToIndex: 2,
		AgentUUID: targetID, Model: "coder-model", Provider: "coder-provider", ContextWindow: 2000,
		UserID: "bob",
	})
	if err != nil {
		t.Fatalf("BranchSession: %v", err)
	}
	if copied != 2 {
		t.Fatalf("copied = %d, want 2", copied)
	}

	got := NewSQLiteSessionStore(db).Get(ctx, newKey) // fresh store: proves what was persisted
	if got == nil {
		t.Fatal("reloaded branch = nil")
	}
	if got.AgentUUID != targetID {
		t.Errorf("AgentUUID = %v, want target %v", got.AgentUUID, targetID)
	}
	if got.Model != "coder-model" || got.Provider != "coder-provider" {
		t.Errorf("target agent runtime not applied: %q/%q", got.Model, got.Provider)
	}
	// context_window is a cache-only field (not a sessions column); it is checked on the
	// in-memory branch below. Listings read the persisted value from the agents table.
	if branch.ContextWindow != 2000 {
		t.Errorf("in-memory ContextWindow = %d, want 2000 (target agent)", branch.ContextWindow)
	}
	if got.Summary != "" {
		t.Errorf("cross-agent branch must not inherit source summary, got %q", got.Summary)
	}
	if got.UserID != "bob" {
		t.Errorf("UserID = %q, want caller bob", got.UserID)
	}
	if len(got.Messages) != 2 || got.Messages[0].ID != srcIDs[0].ID || got.Messages[1].ID != srcIDs[1].ID {
		t.Errorf("message IDs must be preserved across the branch: %+v vs %+v", got.Messages, srcIDs)
	}
}
