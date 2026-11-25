//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// SQLiteMSTeamsConversationStore implements store.MSTeamsConversationStore backed by SQLite.
type SQLiteMSTeamsConversationStore struct {
	db *sql.DB
}

// NewSQLiteMSTeamsConversationStore constructs a SQLite-backed MSTeams conversation store.
func NewSQLiteMSTeamsConversationStore(db *sql.DB) *SQLiteMSTeamsConversationStore {
	return &SQLiteMSTeamsConversationStore{db: db}
}

const msTeamsSQLiteCols = `tenant_id, channel_instance, conversation_id, kind, service_url, ms_tenant_id, team_id, teams_channel_id, user_aad_id, title, updated_at`

// parseMSTeamsTime parses RFC3339/RFC3339Nano timestamps stored as TEXT in SQLite.
func parseMSTeamsTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Upsert inserts or replaces a conversation reference.
func (s *SQLiteMSTeamsConversationStore) Upsert(ctx context.Context, conv *store.MSTeamsConversation) error {
	if conv == nil {
		return errors.New("msteams_conversations: nil conversation")
	}
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return errors.New("msteams_conversations: tenant_id required")
	}
	if conv.ChannelInstance == "" || conv.ConversationID == "" {
		return errors.New("msteams_conversations: channel_instance and conversation_id required")
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO msteams_conversations
    (tenant_id, channel_instance, conversation_id, kind, service_url, ms_tenant_id, team_id, teams_channel_id, user_aad_id, title, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (tenant_id, channel_instance, conversation_id) DO UPDATE SET
    kind             = excluded.kind,
    service_url      = excluded.service_url,
    ms_tenant_id     = excluded.ms_tenant_id,
    team_id          = excluded.team_id,
    teams_channel_id = excluded.teams_channel_id,
    user_aad_id      = excluded.user_aad_id,
    title            = excluded.title,
    updated_at       = CURRENT_TIMESTAMP`,
		tenantID.String(),
		conv.ChannelInstance,
		conv.ConversationID,
		string(conv.Kind),
		conv.ServiceURL,
		conv.MSTenantID,
		conv.TeamID,
		conv.TeamsChannelID,
		conv.UserAADID,
		conv.Title,
	)
	return err
}

// Get retrieves a single conversation by channel instance + conversation ID.
func (s *SQLiteMSTeamsConversationStore) Get(ctx context.Context, channelInstance, conversationID string) (*store.MSTeamsConversation, error) {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return nil, errors.New("msteams_conversations: tenant_id required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+msTeamsSQLiteCols+` FROM msteams_conversations WHERE tenant_id = ? AND channel_instance = ? AND conversation_id = ?`,
		tenantID.String(), channelInstance, conversationID,
	)
	return s.scanRow(row)
}

// FindPersonalByUser returns the personal-scope conversation for a given AAD user ID.
func (s *SQLiteMSTeamsConversationStore) FindPersonalByUser(ctx context.Context, channelInstance, aadID string) (*store.MSTeamsConversation, error) {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return nil, errors.New("msteams_conversations: tenant_id required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+msTeamsSQLiteCols+` FROM msteams_conversations WHERE tenant_id = ? AND channel_instance = ? AND user_aad_id = ? AND kind = ?`,
		tenantID.String(), channelInstance, aadID, string(store.MSTeamsConvPersonal),
	)
	return s.scanRow(row)
}

// List returns all conversations for a channel instance.
func (s *SQLiteMSTeamsConversationStore) List(ctx context.Context, channelInstance string) ([]store.MSTeamsConversation, error) {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return nil, errors.New("msteams_conversations: tenant_id required")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+msTeamsSQLiteCols+` FROM msteams_conversations WHERE tenant_id = ? AND channel_instance = ? ORDER BY updated_at DESC`,
		tenantID.String(), channelInstance,
	)
	if err != nil {
		return nil, err
	}
	return s.scanRows(rows)
}

// DeleteByInstance removes all conversation records for a channel instance.
func (s *SQLiteMSTeamsConversationStore) DeleteByInstance(ctx context.Context, channelInstance string) error {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return errors.New("msteams_conversations: tenant_id required")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM msteams_conversations WHERE tenant_id = ? AND channel_instance = ?`,
		tenantID.String(), channelInstance,
	)
	return err
}

func (s *SQLiteMSTeamsConversationStore) scanRow(row *sql.Row) (*store.MSTeamsConversation, error) {
	var (
		conv         store.MSTeamsConversation
		tidStr       string
		kindStr      string
		updatedAtStr string
	)
	err := row.Scan(
		&tidStr,
		&conv.ChannelInstance,
		&conv.ConversationID,
		&kindStr,
		&conv.ServiceURL,
		&conv.MSTenantID,
		&conv.TeamID,
		&conv.TeamsChannelID,
		&conv.UserAADID,
		&conv.Title,
		&updatedAtStr,
	)
	if err != nil {
		return nil, err
	}
	if tid, err := uuid.Parse(tidStr); err == nil {
		conv.TenantID = tid
	}
	conv.Kind = store.MSTeamsConvKind(kindStr)
	conv.UpdatedAt = parseMSTeamsTime(updatedAtStr)
	return &conv, nil
}

func (s *SQLiteMSTeamsConversationStore) scanRows(rows *sql.Rows) ([]store.MSTeamsConversation, error) {
	defer rows.Close()
	var result []store.MSTeamsConversation
	for rows.Next() {
		var (
			conv         store.MSTeamsConversation
			tidStr       string
			kindStr      string
			updatedAtStr string
		)
		if err := rows.Scan(
			&tidStr,
			&conv.ChannelInstance,
			&conv.ConversationID,
			&kindStr,
			&conv.ServiceURL,
			&conv.MSTenantID,
			&conv.TeamID,
			&conv.TeamsChannelID,
			&conv.UserAADID,
			&conv.Title,
			&updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan msteams_conversations row: %w", err)
		}
		if tid, err := uuid.Parse(tidStr); err == nil {
			conv.TenantID = tid
		}
		conv.Kind = store.MSTeamsConvKind(kindStr)
		conv.UpdatedAt = parseMSTeamsTime(updatedAtStr)
		result = append(result, conv)
	}
	return result, rows.Err()
}
