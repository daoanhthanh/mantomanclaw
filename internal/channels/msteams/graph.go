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
	for part := range strings.SplitSeq(siteID, ",") {
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

// SharepointItem describes one entry returned by listFolder.
type SharepointItem struct {
	Name     string `json:"name"`
	IsFolder bool   `json:"is_folder"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

const maxListFolderItems = 200

// listFolder lists up to maxListFolderItems children of folderPath inside driveID.
// folderPath "/" or "" means the drive root.
func (g *graphClient) listFolder(ctx context.Context, driveID, folderPath string) ([]SharepointItem, error) {
	tok, err := g.tokens.Token(ctx, graphScope)
	if err != nil {
		return nil, fmt.Errorf("graph: get token: %w", err)
	}

	var apiURL string
	cleanPath := strings.Trim(folderPath, "/")
	if cleanPath == "" {
		apiURL = fmt.Sprintf(
			"https://graph.microsoft.com/v1.0/drives/%s/root/children?$select=name,size,file,folder,lastModifiedDateTime&$top=%d",
			driveID, maxListFolderItems,
		)
	} else {
		apiURL = fmt.Sprintf(
			"https://graph.microsoft.com/v1.0/drives/%s/root:/%s:/children?$select=name,size,file,folder,lastModifiedDateTime&$top=%d",
			driveID, strings.TrimPrefix(cleanPath, "/"), maxListFolderItems,
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("graph: build list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph list HTTP %d: %s", resp.StatusCode, body)
	}

	var page struct {
		Value []struct {
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			Modified string `json:"lastModifiedDateTime"`
			File     *struct {
				MimeType string `json:"mimeType"`
			} `json:"file"`
			Folder *struct{} `json:"folder"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("graph: parse list response: %w", err)
	}

	items := make([]SharepointItem, len(page.Value))
	for i, v := range page.Value {
		items[i] = SharepointItem{
			Name:     v.Name,
			IsFolder: v.Folder != nil,
			Size:     v.Size,
			Modified: v.Modified,
		}
	}
	return items, nil
}

// readFileContent fetches the content of the file at filePath inside driveID.
// Returns raw bytes and the MIME type. Errors if the item is a folder or exceeds maxFileSize.
func (g *graphClient) readFileContent(ctx context.Context, driveID, filePath string) ([]byte, string, error) {
	tok, err := g.tokens.Token(ctx, graphScope)
	if err != nil {
		return nil, "", fmt.Errorf("graph: get token: %w", err)
	}

	// Fetch metadata first to check size and confirm it is a file.
	cleanPath := strings.TrimLeft(strings.Trim(filePath, "/"), "/")
	metaURL := fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/drives/%s/root:/%s?$select=id,name,size,file",
		driveID, cleanPath,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("graph: build meta request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	metaBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("graph meta HTTP %d: %s", resp.StatusCode, metaBody)
	}

	var meta struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
		File *struct {
			MimeType string `json:"mimeType"`
		} `json:"file"`
	}
	if err := json.Unmarshal(metaBody, &meta); err != nil {
		return nil, "", fmt.Errorf("graph: parse meta response: %w", err)
	}
	if meta.File == nil {
		return nil, "", fmt.Errorf("%q is a folder, not a file", filePath)
	}
	if meta.Size > maxFileSize {
		return nil, "", fmt.Errorf("file too large: %d bytes (max %d)", meta.Size, maxFileSize)
	}

	mimeType := "application/octet-stream"
	if meta.File.MimeType != "" {
		mimeType = meta.File.MimeType
	}

	// Download content via drives/{id}/items/{itemID}/content.
	contentURL := fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/drives/%s/items/%s/content",
		driveID, meta.ID,
	)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, contentURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("graph: build content request: %w", err)
	}
	req2.Header.Set("Authorization", "Bearer "+tok)

	resp2, err := g.httpClient.Do(req2)
	if err != nil {
		return nil, "", err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp2.Body, 256))
		return nil, "", fmt.Errorf("graph content HTTP %d: %s", resp2.StatusCode, preview)
	}

	content, err := io.ReadAll(io.LimitReader(resp2.Body, maxFileSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("graph: read content: %w", err)
	}
	return content, mimeType, nil
}
