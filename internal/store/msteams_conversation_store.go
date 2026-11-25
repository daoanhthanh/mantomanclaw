package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MSTeamsConvKind identifies conversation scope.
type MSTeamsConvKind string

const (
	MSTeamsConvPersonal  MSTeamsConvKind = "personal"
	MSTeamsConvGroupChat MSTeamsConvKind = "groupChat"
	MSTeamsConvChannel   MSTeamsConvKind = "channel"
)

// MSTeamsConversation is a stored conversation reference.
type MSTeamsConversation struct {
	TenantID        uuid.UUID       `db:"tenant_id"`
	ChannelInstance string          `db:"channel_instance"`
	ConversationID  string          `db:"conversation_id"`
	Kind            MSTeamsConvKind `db:"kind"`
	ServiceURL      string          `db:"service_url"`
	MSTenantID      string          `db:"ms_tenant_id"`
	TeamID          *string         `db:"team_id"`
	TeamsChannelID  *string         `db:"teams_channel_id"`
	UserAADID       *string         `db:"user_aad_id"`
	Title           *string         `db:"title"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

// MSTeamsConversationStore persists Teams conversation references for proactive sends.
type MSTeamsConversationStore interface {
	Upsert(ctx context.Context, conv *MSTeamsConversation) error
	Get(ctx context.Context, channelInstance, conversationID string) (*MSTeamsConversation, error)
	FindPersonalByUser(ctx context.Context, channelInstance, aadID string) (*MSTeamsConversation, error)
	List(ctx context.Context, channelInstance string) ([]MSTeamsConversation, error)
	DeleteByInstance(ctx context.Context, channelInstance string) error
}
