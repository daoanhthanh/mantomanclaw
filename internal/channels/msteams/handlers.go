package msteams

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/systemmessages"
)

const (
	dedupTTL            = 24 * time.Hour
	dedupCleanEvery     = 5 * time.Minute
	defaultThreadTTL    = 10 * time.Minute
	pairingDebounceTime = 60 * time.Second
)

// threadKey mirrors Mattermost's pattern: channelID + ":thread:" + rootID
func threadKey(channelID, rootID string) string {
	return channelID + ":thread:" + rootID
}

// dispatchActivity is called by Channel.handleActivity for every verified activity.
func dispatchActivity(ctx context.Context, ch *Channel, body []byte) {
	var act Activity
	if err := json.Unmarshal(body, &act); err != nil {
		slog.Debug("msteams: skip malformed activity", "error", err)
		return
	}
	switch act.Type {
	case "message":
		ch.handleMessage(ctx, &act)
	case "conversationUpdate":
		ch.handleConversationUpdate(ctx, &act)
	// messageReaction and invoke are handled in Phase 4 (fileConsent).
	}
}

func (ch *Channel) handleConversationUpdate(ctx context.Context, act *Activity) {
	// Upsert conversation ref when bot is added to a team/chat.
	for _, member := range act.MembersAdded {
		if member.ID == act.Recipient.ID {
			ch.upsertConvRef(ctx, act)
			return
		}
	}
}

func (ch *Channel) handleMessage(ctx context.Context, act *Activity) {
	// Tenant check: activity must be from the configured tenant.
	msTenantID := act.ChannelData.Tenant.ID
	if msTenantID == "" {
		msTenantID = act.Conversation.TenantID
	}
	if msTenantID != ch.creds.TenantID {
		slog.Warn("security.msteams.tenant_mismatch",
			"expected", ch.creds.TenantID,
			"got", msTenantID,
			"activity_id", act.ID)
		return
	}

	// Ignore the bot's own messages.
	if act.From.ID == act.Recipient.ID || strings.ToLower(act.From.Role) == "bot" {
		return
	}

	// Dedup on activity ID.
	if _, loaded := ch.dedup.LoadOrStore(act.ID, time.Now()); loaded {
		return
	}

	// Upsert conversation reference so proactive sends can resolve serviceURL.
	ch.upsertConvRef(ctx, act)

	senderID := act.From.AADObjectID
	if senderID == "" {
		senderID = act.From.ID
	}
	senderName := act.From.Name

	convType := act.Conversation.ConversationType // personal | groupChat | channel
	var peerKind, chatID string
	switch convType {
	case "personal":
		peerKind = "direct"
		chatID = act.Conversation.ID
	case "groupChat":
		peerKind = "group"
		chatID = act.Conversation.ID
	case "channel":
		peerKind = "group"
		// Compose a thread key so each Teams channel thread gets its own session.
		teamsChannelID := act.ChannelData.Channel.ID
		rootID := rootMessageID(act)
		if rootID != "" {
			chatID = threadKey(teamsChannelID, rootID)
		} else {
			chatID = teamsChannelID
		}
	default:
		peerKind = "direct"
		chatID = act.Conversation.ID
	}

	// Strip bot mention from text then normalise HTML.
	text := stripBotMention(act)
	text = normalizeHTML(text)

	// Mention gating for group/channel scopes.
	requireMention := ch.cfg.RequireMention != nil && *ch.cfg.RequireMention
	if requireMention && peerKind == "group" {
		mentioned := botMentioned(act)

		// Thread participation window: auto-reply within threadTTL even without @mention.
		if !mentioned && ch.threadTTL > 0 {
			if last, ok := ch.participation.Load(chatID); ok {
				if time.Since(last.(time.Time)) < ch.threadTTL {
					mentioned = true
				} else {
					ch.participation.Delete(chatID)
				}
			}
		}

		if !mentioned {
			if gh := ch.BaseChannel.GroupHistory(); gh != nil {
				gh.Record(chatID, channels.HistoryEntry{
					Sender:    senderName,
					SenderID:  senderID,
					Body:      text,
					MessageID: act.ID,
					Timestamp: time.Now(),
				}, ch.BaseChannel.HistoryLimit())
			}
			if cc := ch.BaseChannel.ContactCollector(); cc != nil {
				cc.EnsureContact(ctx,
					channels.TypeMSTeams, ch.Name(),
					senderID, senderID, senderName, "",
					"group", "user", "", "")
			}
			return
		}
	}

	ctx = store.WithTenantID(ctx, ch.TenantID())

	// Policy checks.
	switch peerKind {
	case "direct":
		result := ch.BaseChannel.CheckDMPolicy(ctx, senderID, ch.cfg.DMPolicy)
		switch result {
		case channels.PolicyDeny:
			return
		case channels.PolicyNeedsPairing:
			ch.sendPairingReply(ctx, act, senderID, false)
			return
		}
	case "group":
		result := ch.BaseChannel.CheckGroupPolicy(ctx, senderID, chatID, ch.cfg.GroupPolicy)
		switch result {
		case channels.PolicyDeny:
			return
		case channels.PolicyNeedsPairing:
			ch.sendPairingReply(ctx, act, senderID, true)
			return
		}
	}

	// Record participation for the thread TTL window.
	ch.markParticipation(chatID)

	// Collect contact.
	if cc := ch.BaseChannel.ContactCollector(); cc != nil {
		cc.EnsureContact(ctx,
			channels.TypeMSTeams, ch.Name(),
			senderID, senderID, senderName, "",
			peerKind, convType, "", "")
	}

	metadata := map[string]string{
		"message_id":   act.ID,
		"conv_type":    convType,
		"ms_tenant_id": msTenantID,
	}
	if act.ChannelData.Channel.ID != "" {
		metadata["teams_channel_id"] = act.ChannelData.Channel.ID
	}
	if act.ReplyToID != "" {
		metadata["reply_to_id"] = act.ReplyToID
	}

	slog.Debug("msteams message received",
		"sender_id", senderID, "chat_id", chatID,
		"peer_kind", peerKind, "preview", channels.Truncate(text, 50))

	// Media is handled in Phase 5; pass nil for now.
	ch.BaseChannel.HandleAuthorizedMessageMedia(senderID, chatID, text, nil, metadata, peerKind)
}

// rootMessageID returns the root message ID for a channel-scoped activity.
// Teams conversation.id for channel activities ends in ";messageid=<id>".
func rootMessageID(act *Activity) string {
	if act.ReplyToID != "" {
		return act.ReplyToID
	}
	// Strip ;messageid= suffix from conversation.id.
	if idx := strings.Index(act.Conversation.ID, ";messageid="); idx >= 0 {
		return act.Conversation.ID[idx+len(";messageid="):]
	}
	return ""
}

// botMentioned returns true if the recipient bot is listed in the activity entities.
func botMentioned(act *Activity) bool {
	for _, ent := range act.Entities {
		if ent.Type == "mention" && ent.Mentioned.ID == act.Recipient.ID {
			return true
		}
	}
	return false
}

// stripBotMention removes the bot's <at> mention tag from the activity text.
func stripBotMention(act *Activity) string {
	text := act.Text
	for _, ent := range act.Entities {
		if ent.Type == "mention" && ent.Mentioned.ID == act.Recipient.ID && ent.Text != "" {
			text = strings.ReplaceAll(text, ent.Text, "")
		}
	}
	return text
}

// isInParticipationWindow reports whether the bot has recently participated in
// chatID within the configured threadTTL.
func (ch *Channel) isInParticipationWindow(chatID string) bool {
	if ch.threadTTL <= 0 {
		return false
	}
	if last, ok := ch.participation.Load(chatID); ok {
		if time.Since(last.(time.Time)) < ch.threadTTL {
			return true
		}
		ch.participation.Delete(chatID)
	}
	return false
}

// markParticipation records the current time for chatID so subsequent
// messages in the same thread are auto-replied within threadTTL.
func (ch *Channel) markParticipation(chatID string) {
	ch.participation.Store(chatID, time.Now())
}

// sendSystemMessage posts a plain-text reply to the conversation that produced act.
// Phase 4 extends this with adaptive-card and streaming support.
func (ch *Channel) sendSystemMessage(ctx context.Context, act *Activity, msg string) {
	if ch.connector == nil || act.ServiceURL == "" || act.Conversation.ID == "" {
		return
	}
	activity := map[string]any{
		"type": "message",
		"text": msg,
	}
	if act.ID != "" {
		activity["replyToId"] = act.ID
	}
	if _, err := ch.connector.postActivity(ctx, act.ServiceURL, act.Conversation.ID, activity); err != nil {
		slog.Warn("msteams: sendSystemMessage failed",
			"error", err, "conv_id", act.Conversation.ID)
	}
}

// sendPairingReply generates a pairing code and sends the appropriate pairing
// prompt to the sender. isGroup selects the group-approval template instead of
// the per-user DM template.
func (ch *Channel) sendPairingReply(ctx context.Context, act *Activity, senderID string, isGroup bool) {
	ps := ch.PairingService()
	if ps == nil {
		return
	}
	if !ch.CanSendPairingNotif(senderID, pairingDebounceTime) {
		return
	}

	code, err := ps.RequestPairing(ctx, senderID, ch.Name(), act.Conversation.ID, "default", nil)
	if err != nil {
		slog.Warn("msteams: RequestPairing failed", "error", err, "sender_id", senderID)
		return
	}
	var msg string
	if isGroup {
		msg = ch.SystemMessage("", systemmessages.KeyPairingGroupPrivateRequired, systemmessages.Vars{
			"platform": "Microsoft Teams",
			"code":     code,
		})
	} else {
		msg = ch.SystemMessage("", systemmessages.KeyPairingAccountRequired, systemmessages.Vars{
			"platform":  "Microsoft Teams",
			"sender_id": senderID,
			"code":      code,
		})
	}

	ch.sendSystemMessage(ctx, act, msg)
	ch.MarkPairingNotifSent(senderID)
}

// upsertConvRef persists the conversation reference extracted from act to the
// store so proactive sends can later resolve the serviceURL and conversation ID.
func (ch *Channel) upsertConvRef(ctx context.Context, act *Activity) {
	if ch.convStore == nil {
		return
	}

	convKind := store.MSTeamsConvKind(act.Conversation.ConversationType)
	switch convKind {
	case store.MSTeamsConvPersonal, store.MSTeamsConvGroupChat, store.MSTeamsConvChannel:
		// valid
	default:
		convKind = store.MSTeamsConvPersonal
	}

	msTenantID := act.ChannelData.Tenant.ID
	if msTenantID == "" {
		msTenantID = act.Conversation.TenantID
	}

	conv := &store.MSTeamsConversation{
		TenantID:        ch.TenantID(),
		ChannelInstance: ch.Name(),
		ConversationID:  act.Conversation.ID,
		Kind:            convKind,
		ServiceURL:      act.ServiceURL,
		MSTenantID:      msTenantID,
		UpdatedAt:       time.Now(),
	}

	if id := act.ChannelData.Team.ID; id != "" {
		conv.TeamID = &id
	}
	if id := act.ChannelData.Channel.ID; id != "" {
		conv.TeamsChannelID = &id
	}
	if id := act.From.AADObjectID; id != "" {
		conv.UserAADID = &id
	}
	if name := act.Conversation.Name; name != "" {
		conv.Title = &name
	}

	ctx = store.WithTenantID(ctx, ch.TenantID())
	if err := ch.convStore.Upsert(ctx, conv); err != nil {
		slog.Warn("msteams: upsertConvRef failed",
			"error", err,
			"conv_id", conv.ConversationID,
			"instance", conv.ChannelInstance)
	}
}

// sweepDedup evicts expired entries from the dedup and participation maps.
// Should be called periodically (e.g. every dedupCleanEvery) from a background goroutine.
func (ch *Channel) sweepDedup() {
	now := time.Now()
	ch.dedup.Range(func(k, v any) bool {
		if t, ok := v.(time.Time); ok && now.Sub(t) > dedupTTL {
			ch.dedup.Delete(k)
		}
		return true
	})
	if ch.threadTTL > 0 {
		ch.participation.Range(func(k, v any) bool {
			if t, ok := v.(time.Time); ok && now.Sub(t) > ch.threadTTL {
				ch.participation.Delete(k)
			}
			return true
		})
	}
}

// PairingMessage is not used externally; pairing replies go through sendPairingReply.
// Defined to satisfy any cross-package reference produced by other phases.
func pairing_unused() { _ = fmt.Sprintf }
