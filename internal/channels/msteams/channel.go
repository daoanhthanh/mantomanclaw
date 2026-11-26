package msteams

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// Compile-time interface assertions.
var (
	_ channels.Channel          = (*Channel)(nil)
	_ channels.ChannelDestroyer = (*Channel)(nil)
	_ channels.StreamingChannel = (*Channel)(nil)
)

// Channel implements the Microsoft Teams native channel.
// It supports personal (DM), group chat, and team channel conversations.
type Channel struct {
	*channels.BaseChannel

	creds  msteamsCreds
	cfg    msteamsInstanceConfig

	verifier  *jwtVerifier
	tokens    *tokenSource
	connector *connectorClient
	graph     *graphClient

	convStore store.MSTeamsConversationStore

	threadTTL time.Duration

	dedup         sync.Map // activity ID -> time.Time
	participation sync.Map // chatID -> time.Time (auto-reply without @mention within threadTTL)

	placeholders sync.Map // chatID/localKey -> placeholderState (activityID of "Thinking...")
	pendingFiles sync.Map // uniqueID -> []byte (file consent pending bytes)
	typingCtrls  sync.Map // localKey -> *typing.Controller (typing indicator while the agent runs)

	stopCh chan struct{}
}

// New validates credentials and constructs a Channel.
func New(creds msteamsCreds, cfg msteamsInstanceConfig,
	msgBus *bus.MessageBus, pairingSvc store.PairingStore,
	convStore store.MSTeamsConversationStore) (*Channel, error) {

	if creds.AppID == "" {
		return nil, fmt.Errorf("msteams: app_id is required")
	}
	if creds.TenantID == "" {
		return nil, fmt.Errorf("msteams: tenant_id is required")
	}
	if creds.ClientSecret == "" && (creds.CertificatePEM == "" || creds.PrivateKeyPEM == "") {
		return nil, fmt.Errorf("msteams: either client_secret or (certificate_pem + private_key_pem) is required")
	}

	ts, err := newTokenSource(creds)
	if err != nil {
		return nil, err
	}

	base := channels.NewBaseChannel(channels.TypeMSTeams, msgBus, cfg.AllowFrom)
	if pairingSvc != nil {
		base.SetPairingService(pairingSvc)
	}

	// Default threadTTL from config; zero disables auto-reply without @mention.
	threadTTL := defaultThreadTTL
	if cfg.ThreadTTL != nil {
		threadTTL = max(time.Duration(*cfg.ThreadTTL)*time.Minute, 0)
	}
	ch := &Channel{
		BaseChannel: base,
		creds:       creds,
		cfg:         cfg,
		verifier:    newJWTVerifier(creds.AppID),
		tokens:      ts,
		connector:   newConnectorClient(ts),
		graph:       newGraphClient(ts, cfg.AllowedSiteIDs, cfg.AllowedHosts),
		convStore:   convStore,
		threadTTL:   threadTTL,
		stopCh:      make(chan struct{}),
	}
	return ch, nil
}

// Factory creates a Channel from DB instance data.
// Implements channels.ChannelFactory.
func Factory(name string, creds json.RawMessage, cfg json.RawMessage,
	msgBus *bus.MessageBus, pairingSvc store.PairingStore) (channels.Channel, error) {

	return FactoryWithStore(nil)(name, creds, cfg, msgBus, pairingSvc)
}

// FactoryWithStore returns a ChannelFactory that injects the conversation store.
func FactoryWithStore(convStore store.MSTeamsConversationStore) channels.ChannelFactory {
	return func(name string, creds json.RawMessage, cfg json.RawMessage,
		msgBus *bus.MessageBus, pairingSvc store.PairingStore) (channels.Channel, error) {

		var c msteamsCreds
		if err := json.Unmarshal(creds, &c); err != nil {
			return nil, fmt.Errorf("msteams: decode credentials: %w", err)
		}

		var ic msteamsInstanceConfig
		if len(cfg) > 0 {
			if err := json.Unmarshal(cfg, &ic); err != nil {
				return nil, fmt.Errorf("msteams: decode config: %w", err)
			}
		}
		// Secure default: require pairing for group messages.
		if ic.GroupPolicy == "" {
			ic.GroupPolicy = "pairing"
		}

		ch, err := New(c, ic, msgBus, pairingSvc, convStore)
		if err != nil {
			return nil, err
		}
		ch.SetName(name)
		globalRouter.register(ch)
		return ch, nil
	}
}

// Start acquires a Bot Connector token to validate credentials, marks healthy.
func (ch *Channel) Start(ctx context.Context) error {
	ch.MarkStarting("connecting")

	_, err := ch.tokens.Token(ctx, botConnectorScope)
	if err != nil {
		ch.MarkFailed("token acquisition failed", err.Error(), channels.ChannelFailureKindAuth, true)
		return fmt.Errorf("msteams: acquire bot connector token: %w", err)
	}

	globalRouter.register(ch)
	ch.MarkHealthy("connected")
	slog.Info("msteams channel started", "name", ch.Name(), "app_id", ch.creds.AppID)
	return nil
}

// Stop unregisters the channel and closes the stop channel.
func (ch *Channel) Stop(_ context.Context) error {
	select {
	case <-ch.stopCh:
		// already stopped
	default:
		close(ch.stopCh)
	}
	globalRouter.unregister(ch.creds.AppID)
	ch.MarkStopped("")
	return nil
}

// Destroy removes conversation store rows for this instance (ChannelDestroyer).
func (ch *Channel) Destroy(ctx context.Context) error {
	if ch.convStore == nil {
		return nil
	}
	if err := ch.convStore.DeleteByInstance(ctx, ch.Name()); err != nil {
		slog.Warn("msteams: destroy: failed to delete conversations",
			"name", ch.Name(), "error", err)
		return err
	}
	return nil
}

// handleActivity is called by the router for each verified inbound activity.
func (ch *Channel) handleActivity(ctx context.Context, body []byte) {
	// Dispatched to handlers.go (Phase 3).
	dispatchActivity(ctx, ch, body)
}
