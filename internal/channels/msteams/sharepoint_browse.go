package msteams

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Compile-time assertion: Channel implements channels.SharepointBrowser.
var _ sharepointBrowserInterface = (*Channel)(nil)

// sharepointBrowserInterface mirrors channels.SharepointBrowser to avoid an
// import cycle (msteams → channels → msteams). The channels.Manager uses a
// type assertion to channels.SharepointBrowser which is defined there.
type sharepointBrowserInterface interface {
	BrowseSharepoint(ctx context.Context, action, driveLabel, path string) (string, error)
}

const spMaxTextBytes = 500_000

// BrowseSharepoint implements channels.SharepointBrowser.
// action: "list_drives" | "list_folder" | "read_file"
func (ch *Channel) BrowseSharepoint(ctx context.Context, action, driveLabel, path string) (string, error) {
	drives := ch.cfg.SharepointDrives
	if len(drives) == 0 {
		return "", fmt.Errorf("no sharepoint_drives configured for this channel")
	}

	switch action {
	case "list_drives":
		return ch.spListDrives()
	case "list_folder":
		drive, err := ch.resolveDrive(driveLabel)
		if err != nil {
			return "", err
		}
		if err := ch.spCheckPath(drive, path); err != nil {
			return "", err
		}
		items, err := ch.graph.listFolder(ctx, drive.DriveID, path)
		if err != nil {
			return "", err
		}
		return ch.spFolderJSON(drive.Label, path, items)
	case "read_file":
		drive, err := ch.resolveDrive(driveLabel)
		if err != nil {
			return "", err
		}
		if err := ch.spCheckPath(drive, path); err != nil {
			return "", err
		}
		content, mimeType, err := ch.graph.readFileContent(ctx, drive.DriveID, path)
		if err != nil {
			return "", err
		}
		return ch.spFileContent(path, mimeType, content)
	default:
		return "", fmt.Errorf("unknown action %q: use list_drives, list_folder, or read_file", action)
	}
}

// resolveDrive returns the drive whose Label matches driveLabel (case-insensitive).
// If driveLabel is empty, returns the first drive.
func (ch *Channel) resolveDrive(driveLabel string) (SharepointDriveConfig, error) {
	drives := ch.cfg.SharepointDrives
	if driveLabel == "" {
		return drives[0], nil
	}
	lower := strings.ToLower(driveLabel)
	for _, d := range drives {
		if strings.ToLower(d.Label) == lower {
			return d, nil
		}
	}
	labels := make([]string, len(drives))
	for i, d := range drives {
		labels[i] = d.Label
	}
	return SharepointDriveConfig{}, fmt.Errorf("drive label %q not found; available: %s", driveLabel, strings.Join(labels, ", "))
}

// spCheckPath verifies that path stays within the drive's RootPath.
func (ch *Channel) spCheckPath(drive SharepointDriveConfig, path string) error {
	root := strings.TrimRight(drive.RootPath, "/")
	if root == "" || root == "/" {
		return nil
	}
	clean := "/" + strings.Trim(path, "/")
	if !strings.HasPrefix(clean+"/", root+"/") && clean != root {
		return fmt.Errorf("path %q is outside configured root %q for drive %q", path, drive.RootPath, drive.Label)
	}
	return nil
}

// spListDrives returns a JSON summary of configured drives.
func (ch *Channel) spListDrives() (string, error) {
	type driveOut struct {
		Label    string `json:"label"`
		DriveID  string `json:"drive_id"`
		RootPath string `json:"root_path"`
	}
	out := make([]driveOut, len(ch.cfg.SharepointDrives))
	for i, d := range ch.cfg.SharepointDrives {
		rp := d.RootPath
		if rp == "" {
			rp = "/"
		}
		out[i] = driveOut{Label: d.Label, DriveID: d.DriveID, RootPath: rp}
	}
	b, _ := json.Marshal(map[string]any{"drives": out})
	return string(b), nil
}

// spFolderJSON serialises a folder listing.
func (ch *Channel) spFolderJSON(driveLabel, path string, items []SharepointItem) (string, error) {
	b, err := json.Marshal(map[string]any{
		"drive": driveLabel,
		"path":  path,
		"count": len(items),
		"items": items,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// spFileContent returns the file content as text, or an error for binary files.
func (ch *Channel) spFileContent(path, mimeType string, content []byte) (string, error) {
	// Refuse obvious binary MIME types so the agent doesn't receive garbage.
	lower := strings.ToLower(mimeType)
	if strings.HasPrefix(lower, "image/") ||
		strings.HasPrefix(lower, "audio/") ||
		strings.HasPrefix(lower, "video/") ||
		lower == "application/octet-stream" ||
		lower == "application/zip" ||
		lower == "application/x-zip-compressed" {
		return "", fmt.Errorf("file %q has binary MIME type %q — use read_file tool for media files", path, mimeType)
	}

	truncated := false
	if len(content) > spMaxTextBytes {
		content = content[:spMaxTextBytes]
		truncated = true
	}

	// Reject non-UTF-8 (binary) content.
	if !utf8.Valid(content) {
		return "", fmt.Errorf("file %q is not valid UTF-8 — it may be a binary file", path)
	}

	text := string(content)
	if truncated {
		text += fmt.Sprintf("\n\n…[truncated, content exceeds %d bytes]", spMaxTextBytes)
	}
	return text, nil
}
