package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// PGMSTeamsConversationStore implements store.MSTeamsConversationStore backed by Postgres.
type PGMSTeamsConversationStore struct {
	db *sql.DB
}

// NewPGMSTeamsConversationStore constructs a Postgres-backed MSTeams conversation store.
func NewPGMSTeamsConversationStore(db *sql.DB) *PGMSTeamsConversationStore {
	return &PGMSTeamsConversationStore{db: db}
}

const msTeamsConvCols = `tenant_id, channel_instance, conversation_id, kind, service_url, ms_tenant_id, team_id, teams_channel_id, user_aad_id, title, updated_at`

// Upsert inserts or updates a conversation reference.
func (s *PGMSTeamsConversationStore) Upsert(ctx context.Context, conv *store.MSTeamsConversation) error {
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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
ON CONFLICT (tenant_id, channel_instance, conversation_id) DO UPDATE SET
    kind             = EXCLUDED.kind,
    service_url      = EXCLUDED.service_url,
    ms_tenant_id     = EXCLUDED.ms_tenant_id,
    team_id          = EXCLUDED.team_id,
    teams_channel_id = EXCLUDED.teams_channel_id,
    user_aad_id      = EXCLUDED.user_aad_id,
    title            = EXCLUDED.title,
    updated_at       = NOW()`,
		tenantID,
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
func (s *PGMSTeamsConversationStore) Get(ctx context.Context, channelInstance, conversationID string) (*store.MSTeamsConversation, error) {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return nil, errors.New("msteams_conversations: tenant_id required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+msTeamsConvCols+` FROM msteams_conversations WHERE tenant_id = $1 AND channel_instance = $2 AND conversation_id = $3`,
		tenantID, channelInstance, conversationID,
	)
	return s.scanRow(row)
}

// FindPersonalByUser returns the personal-scope conversation for a given AAD user ID.
func (s *PGMSTeamsConversationStore) FindPersonalByUser(ctx context.Context, channelInstance, aadID string) (*store.MSTeamsConversation, error) {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return nil, errors.New("msteams_conversations: tenant_id required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+msTeamsConvCols+` FROM msteams_conversations WHERE tenant_id = $1 AND channel_instance = $2 AND user_aad_id = $3 AND kind = $4`,
		tenantID, channelInstance, aadID, string(store.MSTeamsConvPersonal),
	)
	return s.scanRow(row)
}

// List returns all conversations for a channel instance.
func (s *PGMSTeamsConversationStore) List(ctx context.Context, channelInstance string) ([]store.MSTeamsConversation, error) {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return nil, errors.New("msteams_conversations: tenant_id required")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+msTeamsConvCols+` FROM msteams_conversations WHERE tenant_id = $1 AND channel_instance = $2 ORDER BY updated_at DESC`,
		tenantID, channelInstance,
	)
	if err != nil {
		return nil, err
	}
	return s.scanRows(rows)
}

// DeleteByInstance removes all conversation records for a channel instance.
func (s *PGMSTeamsConversationStore) DeleteByInstance(ctx context.Context, channelInstance string) error {
	tenantID := store.TenantIDFromContext(ctx)
	if tenantID == uuid.Nil {
		return errors.New("msteams_conversations: tenant_id required")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM msteams_conversations WHERE tenant_id = $1 AND channel_instance = $2`,
		tenantID, channelInstance,
	)
	return err
}

func (s *PGMSTeamsConversationStore) scanRow(row *sql.Row) (*store.MSTeamsConversation, error) {
	var (
		conv    store.MSTeamsConversation
		kindStr string
	)
	err := row.Scan(
		&conv.TenantID,
		&conv.ChannelInstance,
		&conv.ConversationID,
		&kindStr,
		&conv.ServiceURL,
		&conv.MSTenantID,
		&conv.TeamID,
		&conv.TeamsChannelID,
		&conv.UserAADID,
		&conv.Title,
		&conv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	conv.Kind = store.MSTeamsConvKind(kindStr)
	return &conv, nil
}

func (s *PGMSTeamsConversationStore) scanRows(rows *sql.Rows) ([]store.MSTeamsConversation, error) {
	defer rows.Close()
	var result []store.MSTeamsConversation
	for rows.Next() {
		var (
			conv    store.MSTeamsConversation
			kindStr string
		)
		if err := rows.Scan(
			&conv.TenantID,
			&conv.ChannelInstance,
			&conv.ConversationID,
			&kindStr,
			&conv.ServiceURL,
			&conv.MSTenantID,
			&conv.TeamID,
			&conv.TeamsChannelID,
			&conv.UserAADID,
			&conv.Title,
			&conv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan msteams_conversations row: %w", err)
		}
		conv.Kind = store.MSTeamsConvKind(kindStr)
		result = append(result, conv)
	}
	return result, rows.Err()
}

// Ensure updated_at is consumed even when only used for its side effect.
var _ = time.Now
