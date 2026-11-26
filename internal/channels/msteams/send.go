package msteams

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	maxMessageLen   = 20000
	placeholderText = "Thinking..."
)

// mediaSignerFn is set by gateway wiring via SetMediaSigner so send.go can
// produce signed /v1/files/ URLs without importing internal/http (which
// imports internal/channels, creating a cycle).
var (
	mediaSignerMu sync.RWMutex
	mediaSignerFn func(path string) string
)

// SetMediaSigner wires the gateway's SignMediaPath function into this package.
// Called once from gateway startup before any Send() runs.
func SetMediaSigner(fn func(path string) string) {
	mediaSignerMu.Lock()
	defer mediaSignerMu.Unlock()
	mediaSignerFn = fn
}

func signGatewayMediaURL(path string) string {
	mediaSignerMu.RLock()
	fn := mediaSignerFn
	mediaSignerMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(path)
}

// placeholderState tracks a "Thinking..." placeholder activity.
type placeholderState struct {
	activityID string
	createdAt  time.Time
}

// splitLocalKey splits a local key back into the conversation ID and thread
// root message ID ("" when the key has no thread suffix).
func splitLocalKey(key string) (convID, rootID string) {
	if cid, rid, ok := strings.Cut(key, ":thread:"); ok {
		return cid, rid
	}
	return key, ""
}

func (ch *Channel) storePlaceholder(key, activityID string) {
	ch.placeholders.Store(key, placeholderState{activityID: activityID, createdAt: time.Now()})
}

func (ch *Channel) loadPlaceholder(key string) (string, bool) {
	v, ok := ch.placeholders.Load(key)
	if !ok {
		return "", false
	}
	return v.(placeholderState).activityID, true
}

func (ch *Channel) takePlaceholder(key string) (string, bool) {
	v, ok := ch.placeholders.LoadAndDelete(key)
	if !ok {
		return "", false
	}
	return v.(placeholderState).activityID, true
}

// Send delivers an outbound message to a Teams conversation.
func (ch *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !ch.IsRunning() {
		return fmt.Errorf("msteams bot not running")
	}
	if msg.ChatID == "" {
		return fmt.Errorf("empty chat ID for msteams send")
	}

	convID, keyRootID := splitLocalKey(msg.ChatID)
	rootID := msg.Metadata["message_thread_id"]
	if rootID == "" {
		rootID = keyRootID
	}

	placeholderKey := msg.ChatID
	if pk := msg.Metadata["placeholder_key"]; pk != "" {
		placeholderKey = pk
	}

	svcURL := ch.serviceURL(ctx, convID)

	// Placeholder update (LLM retry notification).
	if msg.Metadata["placeholder_update"] == "true" {
		if actID, ok := ch.loadPlaceholder(placeholderKey); ok && svcURL != "" {
			activity := buildTextActivity(msg.Content, nil, nil)
			if err := ch.connector.putActivity(ctx, svcURL, convID, actID, activity); err != nil {
				slog.Debug("msteams: placeholder update failed", "error", err)
			}
		}
		return nil
	}

	// NO_REPLY: clean up placeholder, done.
	if msg.Content == "" && len(msg.Media) == 0 {
		ch.takePlaceholder(placeholderKey)
		return nil
	}

	text := msg.Content
	_, aadIDs := extractMentions(text)
	entities := buildMentionEntities(aadIDs)

	chunks := channels.ChunkMarkdown(text, maxMessageLen)
	if len(chunks) == 0 {
		chunks = []string{""}
	}

	// Determine target conversation ID. Thread replies need ";messageid=<root>".
	targetConvID := convID
	if rootID != "" {
		targetConvID = convID + ";messageid=" + rootID
	}

	if svcURL == "" {
		slog.Warn("msteams: send: no service URL for conversation", "conv_id", convID)
		return fmt.Errorf("msteams: no service URL for conversation %s", convID)
	}

	// First chunk: edit placeholder if one exists, otherwise post new.
	sent := 0
	if actID, ok := ch.takePlaceholder(placeholderKey); ok {
		activity := buildTextActivity(chunks[0], entities, nil)
		if rootID != "" {
			activity["replyToId"] = rootID
		}
		if err := ch.connector.putActivity(ctx, svcURL, convID, actID, activity); err == nil {
			sent = 1
		} else {
			slog.Warn("msteams: placeholder edit failed, falling back to new message",
				"conv_id", convID, "error", err)
		}
	}

	// Post remaining text chunks.
	for i := sent; i < len(chunks); i++ {
		if chunks[i] == "" {
			continue
		}
		activity := buildTextActivity(chunks[i], entities, nil)
		if rootID != "" {
			activity["replyToId"] = rootID
		}
		if _, err := ch.connector.postActivity(ctx, svcURL, targetConvID, activity); err != nil {
			slog.Warn("msteams: post chunk failed", "conv_id", convID, "chunk", i, "error", err)
			return err
		}
	}

	// Handle media attachments.
	for _, media := range msg.Media {
		if err := ch.sendMedia(ctx, svcURL, targetConvID, rootID, media); err != nil {
			slog.Warn("msteams: media send failed", "file", media.URL, "error", err)
		}
	}

	return nil
}

// buildTextActivity constructs a message activity map.
func buildTextActivity(text string, entities []map[string]any, attachments []map[string]any) map[string]any {
	activity := map[string]any{
		"type":       "message",
		"text":       text,
		"textFormat": "markdown",
	}
	if len(entities) > 0 {
		activity["entities"] = entities
	}
	if len(attachments) > 0 {
		activity["attachments"] = attachments
	}
	return activity
}

// sendMedia delivers a single media attachment to the conversation.
func (ch *Channel) sendMedia(ctx context.Context, svcURL, convID, replyToID string, media bus.MediaAttachment) error {
	mimeType := media.ContentType
	isImage := strings.HasPrefix(mimeType, "image/")

	if isImage {
		dataURL, err := readImageAsDataURL(media.URL, mimeType)
		if err == nil && dataURL != "" {
			attachment := map[string]any{
				"contentType": "image",
				"contentUrl":  dataURL,
			}
			if media.Caption != "" {
				attachment["name"] = media.Caption
			}
			activity := buildTextActivity("", nil, []map[string]any{attachment})
			if replyToID != "" {
				activity["replyToId"] = replyToID
			}
			_, err := ch.connector.postActivity(ctx, svcURL, convID, activity)
			return err
		}
	}

	// Non-image (or data URL failed): link to signed gateway URL.
	signedURL := signGatewayMediaURL(media.URL)
	if signedURL != "" {
		attachment := map[string]any{
			"contentType": mimeType,
			"contentUrl":  signedURL,
		}
		if media.Caption != "" {
			attachment["name"] = media.Caption
		}
		activity := buildTextActivity("", nil, []map[string]any{attachment})
		if replyToID != "" {
			activity["replyToId"] = replyToID
		}
		_, err := ch.connector.postActivity(ctx, svcURL, convID, activity)
		return err
	}

	return fmt.Errorf("msteams: could not deliver media %s", media.URL)
}

// readImageAsDataURL reads a local image file and returns a data URL string.
func readImageAsDataURL(path, mimeType string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if mimeType == "" {
		lower := strings.ToLower(path)
		switch {
		case strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg"):
			mimeType = "image/jpeg"
		case strings.HasSuffix(lower, ".gif"):
			mimeType = "image/gif"
		case strings.HasSuffix(lower, ".webp"):
			mimeType = "image/webp"
		default:
			mimeType = "image/png"
		}
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// serviceURL resolves the Bot Framework service URL for a conversation by
// looking it up in the conversation store.
func (ch *Channel) serviceURL(ctx context.Context, convID string) string {
	if ch.convStore == nil {
		return ""
	}
	// Outbound contexts carry no tenant; the store scopes every query by it.
	ctx = store.WithTenantID(ctx, ch.TenantID())
	conv, err := ch.convStore.Get(ctx, ch.Name(), convID)
	if err != nil {
		slog.Warn("msteams: conversation lookup failed", "conv_id", convID, "error", err)
		return ""
	}
	if conv == nil {
		return ""
	}
	return conv.ServiceURL
}
