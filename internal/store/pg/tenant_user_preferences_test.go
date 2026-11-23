package pg

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestPGUserPreferences_RoundTrip(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, _ := seedTenantAndAgent(t, db)
	s := NewPGTenantStore(db)
	ctx := context.Background()
	user := "prefs-" + uuid.NewString()

	// No row → empty prefs; Get must not create membership.
	got, err := s.GetUserPreferences(ctx, tenantID, user)
	if err != nil || got.DefaultAgentID != nil {
		t.Fatalf("empty get: prefs=%+v err=%v", got, err)
	}
	if role, _ := s.GetUserRole(ctx, tenantID, user); role != "" {
		t.Fatalf("Get created membership, role=%q", role)
	}

	// Set creates the row as a plain member.
	agent := uuid.New()
	if err := s.UpdateUserPreferences(ctx, tenantID, user, &store.UserPreferences{DefaultAgentID: &agent}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.GetUserPreferences(ctx, tenantID, user)
	if got.DefaultAgentID == nil || *got.DefaultAgentID != agent {
		t.Fatalf("round trip: %+v", got)
	}
	if role, _ := s.GetUserRole(ctx, tenantID, user); role != store.TenantRoleMember {
		t.Fatalf("role = %q, want member", role)
	}

	// Update keeps sibling metadata keys and does not downgrade an existing role.
	if _, err := db.Exec(`UPDATE tenant_users SET role = 'admin', metadata = metadata || '{"other":"keep"}'::jsonb WHERE tenant_id = $1 AND user_id = $2`, tenantID, user); err != nil {
		t.Fatal(err)
	}
	agent2 := uuid.New()
	if err := s.UpdateUserPreferences(ctx, tenantID, user, &store.UserPreferences{DefaultAgentID: &agent2}); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	got, _ = s.GetUserPreferences(ctx, tenantID, user)
	if got.DefaultAgentID == nil || *got.DefaultAgentID != agent2 {
		t.Fatalf("second update: %+v", got)
	}
	var other, role string
	if err := db.QueryRow(`SELECT metadata->>'other', role FROM tenant_users WHERE tenant_id = $1 AND user_id = $2`, tenantID, user).Scan(&other, &role); err != nil {
		t.Fatal(err)
	}
	if other != "keep" || role != "admin" {
		t.Fatalf("clobbered: other=%q role=%q", other, role)
	}
}
