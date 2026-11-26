package msteams

import (
	"context"
	"log/slog"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels/typing"
)

const (
	// Teams shows a typing indicator for about 3s; refresh before it lapses.
	teamsTypingKeepalive = 2 * time.Second
	// Safety net so a run that never replies cannot leave the indicator on.
	teamsTypingTTL = 90 * time.Second
)

// startTypingIndicator sends a Bot Framework typing activity to the conversation
// identified by convID/svcURL and keeps it alive with a 2s keepalive.
// The controller is stored in ch.typingCtrls keyed by localKey.
// Best-effort: errors are logged at Debug and never propagate.
func (ch *Channel) startTypingIndicator(ctx context.Context, localKey, svcURL, convID string) {
	if ch.connector == nil || svcURL == "" || convID == "" {
		return
	}

	// Stop any previous indicator for the same key.
	ch.stopTypingIndicator(localKey)

	ctrl := typing.New(typing.Options{
		MaxDuration:       teamsTypingTTL,
		KeepaliveInterval: teamsTypingKeepalive,
		StartFn: func() error {
			return ch.sendTypingActivity(ctx, svcURL, convID)
		},
	})
	ch.typingCtrls.Store(localKey, ctrl)
	ctrl.Start()
}

// stopTypingIndicator stops and removes the typing controller for localKey.
func (ch *Channel) stopTypingIndicator(localKey string) {
	if v, ok := ch.typingCtrls.LoadAndDelete(localKey); ok {
		v.(*typing.Controller).Stop()
	}
}

// sendTypingActivity posts a "typing" activity to the given conversation.
func (ch *Channel) sendTypingActivity(ctx context.Context, svcURL, convID string) error {
	_, err := ch.connector.postActivity(ctx, svcURL, convID, map[string]any{
		"type": "typing",
	})
	if err != nil {
		slog.Debug("msteams: typing activity failed", "conv_id", convID, "error", err)
	}
	return err
}
