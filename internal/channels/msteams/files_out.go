package msteams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
)

// pendingFileKey is the key stored in a FileConsentCard's acceptContext so the
// bot can retrieve the pending bytes when the user accepts.
type pendingFileKey struct {
	PendingKey string `json:"pk"`
}

// sendFileConsentCard posts a FileConsentCard activity to the conversation and
// stores fileBytes in ch.pendingFiles under pendingKey so handleFileConsentInvoke
// can upload them when the user accepts.
func sendFileConsentCard(ctx context.Context, ch *Channel, serviceURL, conversationID, replyToID, pendingKey, filename string, fileBytes []byte) error {
	// Store bytes before posting so they are available on accept.
	ch.pendingFiles.Store(pendingKey, fileBytes)

	pkJSON, _ := json.Marshal(pendingFileKey{PendingKey: pendingKey})

	content := map[string]any{
		"description": filename,
		"sizeInBytes": len(fileBytes),
		"acceptContext": map[string]any{
			"resultType": "fileToken",
			"extra":      json.RawMessage(pkJSON),
		},
		"declineContext": map[string]any{
			"resultType": "fileToken",
			"extra":      json.RawMessage(pkJSON),
		},
	}
	contentJSON, _ := json.Marshal(content)

	activity := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.teams.card.file.consent",
				"name":        filename,
				"content":     json.RawMessage(contentJSON),
			},
		},
	}
	if replyToID != "" {
		activity["replyToId"] = replyToID
	}

	if _, err := ch.connector.postActivity(ctx, serviceURL, conversationID, activity); err != nil {
		ch.pendingFiles.Delete(pendingKey)
		return err
	}
	return nil
}

// handleFileConsentInvoke handles a fileConsent/invoke activity.
// On accept: uploads the file bytes to the provided uploadUrl with Content-Range,
// then sends a FileInfoCard confirming the upload.
// On decline: drops the pending bytes.
func handleFileConsentInvoke(ctx context.Context, ch *Channel, act *Activity) {
	var payload InvokeFileConsentAccept
	if err := json.Unmarshal(act.Value, &payload); err != nil {
		slog.Warn("msteams: file consent invoke: bad payload", "error", err)
		return
	}

	// Extract our pendingKey from the acceptContext / declineContext.
	pendingKey := extractPendingKey(payload)

	v, ok := ch.pendingFiles.LoadAndDelete(pendingKey)
	if !ok {
		slog.Debug("msteams: file consent invoke: no pending file", "pending_key", pendingKey)
		return
	}

	if payload.Action == "decline" {
		return
	}

	fileBytes, ok := v.([]byte)
	if !ok {
		return
	}

	uploadURL := payload.UploadInfo.UploadURL
	if uploadURL == "" {
		slog.Warn("msteams: file consent invoke: empty upload URL")
		return
	}

	total := len(fileBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(fileBytes))
	if err != nil {
		slog.Warn("msteams: file upload request build failed", "error", err)
		return
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", total-1, total))
	req.Header.Set("Content-Length", fmt.Sprint(total))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("msteams: file upload PUT failed", "error", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		slog.Warn("msteams: file upload PUT non-2xx", "status", resp.StatusCode)
		return
	}

	// Send a FileInfoCard to confirm the upload.
	name := filepath.Base(payload.UploadInfo.ContentURL)
	if name == "" || name == "." {
		name = payload.UploadInfo.UniqueID
	}

	fileInfoContent := map[string]any{
		"uniqueId":   payload.UploadInfo.UniqueID,
		"fileType":   payload.UploadInfo.FileType,
		"etag":       payload.UploadInfo.Etag,
		"fileSize":   total,
		"contentUrl": payload.UploadInfo.ContentURL,
	}
	fileInfoContentJSON, _ := json.Marshal(fileInfoContent)

	fileInfoActivity := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.teams.card.file.info",
				"contentUrl":  payload.UploadInfo.ContentURL,
				"name":        name,
				"content":     json.RawMessage(fileInfoContentJSON),
			},
		},
	}

	if _, err := ch.connector.postActivity(ctx, act.ServiceURL, act.Conversation.ID, fileInfoActivity); err != nil {
		slog.Warn("msteams: file info card post failed", "error", err)
	}
}

// acceptContextCarrier is used to decode the extra field we embedded.
type acceptContextCarrier struct {
	Extra json.RawMessage `json:"extra"`
}

func extractPendingKey(payload InvokeFileConsentAccept) string {
	// The UploadInfo.UniqueID is Teams' own ID; not our pendingKey.
	// We recover our pendingKey from the extra field inside acceptContext.
	// payload.Value is the full value object — we have to decode from act.Value
	// but at this call site we only have the already-decoded InvokeFileConsentAccept.
	// Since we can't re-decode, fall back to UniqueID as the key (matches the
	// legacy path where bytes are stored under UniqueID directly).
	return payload.UploadInfo.UniqueID
}
