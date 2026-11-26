package msteams

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

const (
	maxAttachmentsPerMsg  = 5
	attachDownloadTimeout = 30 * time.Second
	attachMaxBytes        = 20 << 20 // 20 MiB
	inboundMediaTTL       = 30 * time.Minute
)

// downloadAttachments downloads up to maxAttachmentsPerMsg attachments from an
// inbound activity to tempDir and returns the resulting media files.
//
// Attachment dispatch:
//   - application/vnd.microsoft.teams.file.download.info → download via the
//     embedded downloadURL (OneDrive/SharePoint host-scoped token).
//   - image/* with contentUrl on smba.trafficmanager.net or *.botframework.com
//     → download with the Bot Connector token.
//   - other types → try the Graph share-link path (resolve + download).
//
// Personal OneDrive hosts (*-my.sharepoint.com) are skipped: the bot lacks
// consent, so we return no file and let the caller emit a marker.
func downloadAttachments(ctx context.Context, ch *Channel, act *Activity, tempDir string) []bus.MediaFile {
	if len(act.Attachments) == 0 {
		return nil
	}

	var out []bus.MediaFile
	for _, att := range act.Attachments {
		if len(out) >= maxAttachmentsPerMsg {
			break
		}
		mf, err := downloadOneAttachment(ctx, ch, att, tempDir)
		if err != nil {
			slog.Debug("msteams: attachment download failed",
				"content_type", att.ContentType, "name", att.Name, "error", err)
			continue
		}
		if mf.Path != "" {
			out = append(out, mf)
		}
	}
	return out
}

// downloadOneAttachment handles a single attachment, returning the downloaded
// media file or an error. A zero-value MediaFile with no Path and no error
// means the attachment was intentionally skipped (e.g. personal OneDrive).
func downloadOneAttachment(ctx context.Context, ch *Channel, att Attachment, tempDir string) (bus.MediaFile, error) {
	switch {
	case att.ContentType == "application/vnd.microsoft.teams.file.download.info":
		return downloadFileDownloadInfo(ctx, ch, att, tempDir)

	case strings.HasPrefix(att.ContentType, "image/"):
		return downloadInlineImage(ctx, ch, att, tempDir)

	default:
		return downloadViaGraph(ctx, ch, att, tempDir)
	}
}

// downloadFileDownloadInfo handles the Teams file-download-info attachment type.
// The attachment Content carries an AttachmentContentFileDownloadInfo with a
// pre-signed downloadURL pointing at SharePoint/OneDrive.
func downloadFileDownloadInfo(ctx context.Context, ch *Channel, att Attachment, tempDir string) (bus.MediaFile, error) {
	if len(att.Content) == 0 {
		return bus.MediaFile{}, fmt.Errorf("file download info has no content")
	}
	var info AttachmentContentFileDownloadInfo
	if err := json.Unmarshal(att.Content, &info); err != nil {
		return bus.MediaFile{}, fmt.Errorf("parse download info: %w", err)
	}
	if info.DownloadURL == "" {
		return bus.MediaFile{}, fmt.Errorf("download info has no downloadUrl")
	}

	u, err := url.Parse(info.DownloadURL)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("parse download url: %w", err)
	}

	// Personal OneDrive hosts (*-my.sharepoint.com) require per-user consent
	// the bot does not have; skip and let the caller emit a marker.
	if strings.HasSuffix(u.Hostname(), "-my.sharepoint.com") {
		slog.Debug("msteams: skipping personal OneDrive file", "host", u.Hostname())
		return bus.MediaFile{}, nil
	}

	// downloadUrl is pre-signed by SharePoint; no bearer token is needed, and
	// withholding it avoids leaking the Graph token to a signed-URL host.
	return downloadURLToFile(ctx, info.DownloadURL, "", att.Name, info.FileType, tempDir, ch.graph.httpClient)
}

// downloadInlineImage downloads an image attachment hosted on the Bot
// Framework CDN (smba.trafficmanager.net / *.botframework.com) using the
// Bot Connector token. Non-CDN image URLs fall through to the Graph path.
func downloadInlineImage(ctx context.Context, ch *Channel, att Attachment, tempDir string) (bus.MediaFile, error) {
	if att.ContentURL == "" {
		// No content URL; try Graph resolution as a fallback.
		return downloadViaGraph(ctx, ch, att, tempDir)
	}

	u, err := url.Parse(att.ContentURL)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("parse content url: %w", err)
	}

	host := u.Hostname()
	isCDN := strings.HasSuffix(host, ".trafficmanager.net") ||
		strings.HasSuffix(host, ".botframework.com")
	if !isCDN {
		return downloadViaGraph(ctx, ch, att, tempDir)
	}

	tok, err := ch.tokens.Token(ctx, botConnectorScope)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("bot connector token: %w", err)
	}

	return downloadURLToFile(ctx, att.ContentURL, tok, att.Name, "", tempDir, ch.graph.httpClient)
}

// downloadViaGraph resolves a SharePoint share link (from attachment contentUrl
// or extracted from message text) through Microsoft Graph and downloads the
// drive item content.
func downloadViaGraph(ctx context.Context, ch *Channel, att Attachment, tempDir string) (bus.MediaFile, error) {
	shareURL := att.ContentURL
	if shareURL == "" {
		return bus.MediaFile{}, fmt.Errorf("no content url for graph resolution")
	}

	u, err := url.Parse(shareURL)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("parse share url: %w", err)
	}
	if isPrivateHost(u.Hostname()) {
		return bus.MediaFile{}, fmt.Errorf("blocked private host: %s", u.Hostname())
	}
	if !isAllowedSharePointHost(u.Hostname()) && !ch.graph.allowedHosts[u.Hostname()] {
		return bus.MediaFile{}, fmt.Errorf("host not allowed: %s", u.Hostname())
	}
	if strings.HasSuffix(u.Hostname(), "-my.sharepoint.com") {
		slog.Debug("msteams: skipping personal OneDrive share", "host", u.Hostname())
		return bus.MediaFile{}, nil
	}

	item, err := ch.graph.resolveShareLink(ctx, shareURL)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("resolve share link: %w", err)
	}

	path, mimeType, err := ch.graph.downloadDriveItem(ctx, item, tempDir)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("download drive item: %w", err)
	}

	return bus.MediaFile{Path: path, MimeType: mimeType, Filename: item.Name}, nil
}

// downloadURLToFile performs an authenticated GET to downloadURL, writing the
// response body (capped at attachMaxBytes) to a temp file in tempDir.
// client is used for its redirect policy; the Authorization header is set per
// request. extHint is used to pick a file extension when name has none.
func downloadURLToFile(ctx context.Context, downloadURL, token, name, extHint, tempDir string, client *http.Client) (bus.MediaFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return bus.MediaFile{}, fmt.Errorf("build download request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	dlClient := client
	if dlClient == nil {
		dlClient = &http.Client{Timeout: attachDownloadTimeout}
	}

	resp, err := dlClient.Do(req)
	if err != nil {
		return bus.MediaFile{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return bus.MediaFile{}, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	ext := filepath.Ext(name)
	if ext == "" && extHint != "" {
		ext = "." + extHint
	}
	if ext == "" {
		ext = ".bin"
	}

	f, err := os.CreateTemp(tempDir, "msteams-*"+ext)
	if err != nil {
		return bus.MediaFile{}, err
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(resp.Body, attachMaxBytes+1)); err != nil {
		os.Remove(f.Name())
		return bus.MediaFile{}, err
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return bus.MediaFile{Path: f.Name(), MimeType: mimeType, Filename: name}, nil
}

// collectInboundMedia downloads Teams file attachments and SharePoint links
// found in the message text, returning them as media for the agent. Files land
// in a private temp dir that is removed after inboundMediaTTL, by which time
// the agent loop has copied them into the user workspace.
func (ch *Channel) collectInboundMedia(ctx context.Context, act *Activity) []bus.MediaFile {
	links := extractSharePointLinks(html.UnescapeString(act.Text))
	if len(act.Attachments) == 0 && len(links) == 0 {
		return nil
	}

	tempDir, err := os.MkdirTemp("", "msteams-media-*")
	if err != nil {
		slog.Warn("msteams: create media temp dir failed", "error", err)
		return nil
	}

	files := downloadAttachments(ctx, ch, act, tempDir)

	// Links already covered by an attachment are not fetched twice.
	attached := make(map[string]bool, len(act.Attachments))
	for _, att := range act.Attachments {
		attached[att.ContentURL] = true
	}
	for _, link := range links {
		if len(files) >= maxAttachmentsPerMsg {
			break
		}
		if attached[link] {
			continue
		}
		mf, err := downloadViaGraph(ctx, ch, Attachment{ContentURL: link}, tempDir)
		if err != nil {
			slog.Warn("msteams: sharepoint link download failed", "error", err)
			continue
		}
		if mf.Path != "" {
			files = append(files, mf)
		}
	}

	if len(files) == 0 {
		os.RemoveAll(tempDir)
		return nil
	}
	time.AfterFunc(inboundMediaTTL, func() { os.RemoveAll(tempDir) })
	return files
}
