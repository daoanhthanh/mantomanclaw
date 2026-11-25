package msteams

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
)

const streamThrottleInterval = 1 * time.Second

// msteamsStream edits the "Thinking..." placeholder activity as chunks arrive.
type msteamsStream struct {
	ch         *Channel
	convID     string
	rootID     string
	activityID string // placeholder activity; empty = no-op stream
	lastUpdate time.Time
	mu         sync.Mutex
}

// Update edits the placeholder with accumulated text, throttled to avoid rate limits.
func (s *msteamsStream) Update(ctx context.Context, fullText string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activityID == "" || time.Since(s.lastUpdate) < streamThrottleInterval {
		return
	}

	text := fullText
	if text == "" {
		return
	}
	if len(text) > maxMessageLen {
		text = channels.ChunkMarkdown(text, maxMessageLen)[0] + "..."
	}

	svcURL := s.ch.serviceURL(ctx, s.convID)
	if svcURL == "" {
		return
	}

	activity := buildTextActivity(text, nil, nil)
	if s.rootID != "" {
		activity["replyToId"] = s.rootID
	}

	if err := s.ch.connector.putActivity(ctx, svcURL, s.convID, s.activityID, activity); err != nil {
		slog.Debug("msteams stream update failed", "error", err)
		return
	}
	s.lastUpdate = time.Now()
}

// Stop is a no-op: Send() writes the final text via the placeholder map after
// FinalizeStream hands the activity ID back.
func (s *msteamsStream) Stop(_ context.Context) error { return nil }

// MessageID returns 0 — Teams activity IDs are strings.
func (s *msteamsStream) MessageID() int { return 0 }

// StreamEnabled reports whether streaming is on for DMs or groups.
func (ch *Channel) StreamEnabled(isGroup bool) bool {
	if isGroup {
		return ch.cfg.GroupStream != nil && *ch.cfg.GroupStream
	}
	return ch.cfg.DMStream != nil && *ch.cfg.DMStream
}

// CreateStream creates a per-run streaming handle bound to the placeholder
// posted for chatID (the local key). Without a placeholder the stream is a no-op.
func (ch *Channel) CreateStream(_ context.Context, chatID string, _ bool) (channels.ChannelStream, error) {
	convID, rootID := splitLocalKey(chatID)
	s := &msteamsStream{ch: ch, convID: convID, rootID: rootID}
	if actID, ok := ch.loadPlaceholder(chatID); ok {
		s.activityID = actID
	}
	return s, nil
}

// FinalizeStream stores the placeholder activity ID back so Send() can edit it
// with the final formatted response.
func (ch *Channel) FinalizeStream(_ context.Context, chatID string, stream channels.ChannelStream) {
	s, ok := stream.(*msteamsStream)
	if !ok || s.activityID == "" {
		return
	}
	ch.storePlaceholder(chatID, s.activityID)
}

// ReasoningStreamEnabled returns false: reasoning is not shown as a separate
// activity in Teams.
func (ch *Channel) ReasoningStreamEnabled() bool { return false }
