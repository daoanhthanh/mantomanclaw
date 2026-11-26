package msteams

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	graphScope      = "https://graph.microsoft.com/.default"
	maxFileSize     = 20 << 20 // 20 MiB
	maxRedirects    = 3
	downloadTimeout = 30 * time.Second
)

// globalAllowedHosts lists hosts always permitted for Graph/SharePoint downloads.
var globalAllowedHosts = map[string]bool{
	"graph.microsoft.com": true,
}

func isAllowedSharePointHost(host string) bool {
	if globalAllowedHosts[host] {
		return true
	}
	// *.sharepoint.com allowed
	return strings.HasSuffix(host, ".sharepoint.com")
}

// isPrivateHost rejects RFC 1918 / link-local / loopback targets (SSRF guard).
// This catches obvious literals; net dial is the final guard.
func isPrivateHost(hostname string) bool {
	switch hostname {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}

// graphClient resolves SharePoint/OneDrive items via Microsoft Graph.
type graphClient struct {
	tokens         *tokenSource
	httpClient     *http.Client
	allowedSiteIDs map[string]bool
	allowedHosts   map[string]bool
}

func newGraphClient(ts *tokenSource, allowedSiteIDs, allowedHosts []string) *graphClient {
	siteMap := make(map[string]bool, len(allowedSiteIDs))
	for _, id := range allowedSiteIDs {
		siteMap[id] = true
	}
	hostMap := make(map[string]bool, len(allowedHosts))
	for _, h := range allowedHosts {
		hostMap[h] = true
	}
	return &graphClient{
		tokens: ts,
		httpClient: &http.Client{
			Timeout: downloadTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return errors.New("too many redirects")
				}
				if !isAllowedSharePointHost(req.URL.Hostname()) {
					return fmt.Errorf("redirect to disallowed host: %s", req.URL.Hostname())
				}
				// Drop Authorization after first hop.
				if len(via) >= 1 {
					req.Header.Del("Authorization")
				}
				return nil
			},
		},
		allowedSiteIDs: siteMap,
		allowedHosts:   hostMap,
	}
}

// resolveShareLink resolves a SharePoint share URL to a drive item.
func (g *graphClient) resolveShareLink(ctx context.Context, shareURL string) (*driveItem, error) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(shareURL))
	apiURL := fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/shares/u!%s/driveItem?$select=id,name,size,file,parentReference,webUrl",
		encoded,
	)

	tok, err := g.tokens.Token(ctx, graphScope)
	if err != nil {
		return nil, fmt.Errorf("graph: get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("graph: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph resolve HTTP %d: %s", resp.StatusCode, body)
	}

	var item driveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, err
	}
	return &item, nil
}

type driveItem struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Size            int64           `json:"size"`
	ParentReference parentReference `json:"parentReference"`
	WebURL          string          `json:"webUrl"`
	File            *struct {
		MimeType string `json:"mimeType"`
	} `json:"file"`
}

type parentReference struct {
	SiteID  string `json:"siteId"`
	DriveID string `json:"driveId"`
}

// siteAllowed reports whether siteID is in the allowlist. Graph returns site
// IDs in composite form "hostname,siteCollectionId,webId", so a configured
// entry may be the full composite or any single component of it.
func (g *graphClient) siteAllowed(siteID string) bool {
	if siteID == "" {
		return false
	}
	if g.allowedSiteIDs[siteID] {
		return true
	}
	for _, part := range strings.Split(siteID, ",") {
		if part != "" && g.allowedSiteIDs[part] {
			return true
		}
	}
	return false
}

// downloadDriveItem downloads the item content to a temp file.
// Returns the local file path, the MIME type, and any error.
func (g *graphClient) downloadDriveItem(ctx context.Context, item *driveItem, tempDir string) (string, string, error) {
	if item.Size > maxFileSize {
		return "", "", fmt.Errorf("file too large: %d bytes (max %d)", item.Size, maxFileSize)
	}

	// Check site allowlist.
	if !g.siteAllowed(item.ParentReference.SiteID) {
		return "", "", fmt.Errorf("site %q not granted to the bot", item.ParentReference.SiteID)
	}

	tok, err := g.tokens.Token(ctx, graphScope)
	if err != nil {
		return "", "", err
	}

	contentURL := fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/drives/%s/items/%s/content",
		item.ParentReference.DriveID, item.ID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, contentURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("graph: build download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	mimeType := ""
	if item.File != nil {
		mimeType = item.File.MimeType
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	ext := filepath.Ext(item.Name)
	f, err := os.CreateTemp(tempDir, "msteams-*"+ext)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxFileSize+1)); err != nil {
		os.Remove(f.Name())
		return "", "", err
	}

	return f.Name(), mimeType, nil
}
