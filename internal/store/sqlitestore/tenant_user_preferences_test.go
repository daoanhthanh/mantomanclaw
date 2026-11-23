//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestUserPreferences_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	s := NewSQLiteTenantStore(db)
	ctx := context.Background()
	tid := store.MasterTenantID

	// No tenant_users row yet → empty prefs, and Get must not create membership.
	got, err := s.GetUserPreferences(ctx, tid, "alice")
	if err != nil || got.DefaultAgentID != nil {
		t.Fatalf("empty get: prefs=%+v err=%v", got, err)
	}
	if role, _ := s.GetUserRole(ctx, tid, "alice"); role != "" {
		t.Fatalf("Get created membership, role=%q", role)
	}

	// Set creates the row as a plain member.
	agent := uuid.New()
	if err := s.UpdateUserPreferences(ctx, tid, "alice", &store.UserPreferences{DefaultAgentID: &agent}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.GetUserPreferences(ctx, tid, "alice")
	if got.DefaultAgentID == nil || *got.DefaultAgentID != agent {
		t.Fatalf("round trip: %+v", got)
	}
	if role, _ := s.GetUserRole(ctx, tid, "alice"); role != store.TenantRoleMember {
		t.Fatalf("role = %q, want member", role)
	}

	// Update must keep sibling metadata keys and must not downgrade an existing role.
	if _, err := db.Exec(`UPDATE tenant_users SET role = 'admin', metadata = json_set(metadata, '$.other', 'keep') WHERE user_id = 'alice'`); err != nil {
		t.Fatal(err)
	}
	agent2 := uuid.New()
	if err := s.UpdateUserPreferences(ctx, tid, "alice", &store.UserPreferences{DefaultAgentID: &agent2}); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	got, _ = s.GetUserPreferences(ctx, tid, "alice")
	if got.DefaultAgentID == nil || *got.DefaultAgentID != agent2 {
		t.Fatalf("second update: %+v", got)
	}
	var other, role string
	if err := db.QueryRow(`SELECT json_extract(metadata,'$.other'), role FROM tenant_users WHERE user_id='alice'`).Scan(&other, &role); err != nil {
		t.Fatal(err)
	}
	if other != "keep" || role != "admin" {
		t.Fatalf("clobbered: other=%q role=%q", other, role)
	}

	// Preferences are per user.
	if bob, _ := s.GetUserPreferences(ctx, tid, "bob"); bob.DefaultAgentID != nil {
		t.Fatalf("bob inherited alice's prefs: %+v", bob)
	}
}
