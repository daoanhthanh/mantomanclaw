package tools

import (
	"context"
	"fmt"
	"strings"
)

// SharepointBrowseFunc calls into a channel instance to browse SharePoint.
// action: "list_drives" | "list_folder" | "read_file"
type SharepointBrowseFunc func(ctx context.Context, channelName, action, driveLabel, path string) (string, error)

// SharepointBrowseFuncAware tools can receive a SharepointBrowseFunc.
type SharepointBrowseFuncAware interface {
	SetSharepointBrowseFunc(SharepointBrowseFunc)
}

// SharepointReadTool lets the agent proactively browse and read SharePoint
// document libraries configured in the msteams channel instance.
type SharepointReadTool struct {
	browser SharepointBrowseFunc
}

// NewSharepointReadTool creates a new sharepoint_read tool.
func NewSharepointReadTool() *SharepointReadTool { return &SharepointReadTool{} }

// SetSharepointBrowseFunc injects the browse function (wired at boot).
func (t *SharepointReadTool) SetSharepointBrowseFunc(fn SharepointBrowseFunc) { t.browser = fn }

// RequiredChannelTypes implements tools.ChannelAware — only visible on MS Teams.
func (t *SharepointReadTool) RequiredChannelTypes() []string { return []string{"msteams"} }

func (t *SharepointReadTool) Name() string { return "sharepoint_read" }

func (t *SharepointReadTool) Description() string {
	return "Browse and read SharePoint document libraries configured for this Microsoft Teams channel. " +
		"Use action='list_drives' to see available drives, action='list_folder' to list a folder's contents, " +
		"and action='read_file' to read a file's text content. " +
		"Paths are relative to the drive root (e.g. '/General/Reports'). " +
		"Binary files (images, videos, zip) are not supported — use SharePoint links shared in chat instead."
}

func (t *SharepointReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list_drives", "list_folder", "read_file"},
				"description": "Action to perform: list_drives=show configured drives, list_folder=list items in a folder, read_file=read file text content.",
			},
			"drive_label": map[string]any{
				"type":        "string",
				"description": "Label of the drive to use (from list_drives). Omit to use the first configured drive.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Folder or file path relative to the drive root. Required for list_folder and read_file. Example: '/General/Reports'.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SharepointReadTool) Execute(ctx context.Context, args map[string]any) *Result {
	if t.browser == nil {
		return ErrorResult("sharepoint_read: not available (no SharePoint browse function configured)")
	}

	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	if action == "" {
		return ErrorResult("action parameter is required (list_drives, list_folder, or read_file)")
	}

	driveLabel, _ := args["drive_label"].(string)
	path, _ := args["path"].(string)

	if (action == "list_folder" || action == "read_file") && strings.TrimSpace(path) == "" {
		return ErrorResult(fmt.Sprintf("path parameter is required for action=%q", action))
	}

	channelName := ToolChannelFromCtx(ctx)
	if channelName == "" {
		return ErrorResult("sharepoint_read: no channel context (not running in a Teams conversation)")
	}

	result, err := t.browser(ctx, channelName, action, driveLabel, path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("sharepoint_read: %v", err))
	}
	return NewResult(result)
}
