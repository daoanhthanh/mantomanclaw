package msteams

import "encoding/json"

// Activity is the Bot Framework Activity object.
// Only fields we use are decoded; unknown fields are silently ignored.
type Activity struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Timestamp    string              `json:"timestamp"`
	ServiceURL   string              `json:"serviceUrl"`
	ChannelID    string              `json:"channelId"` // always "msteams"
	From         ChannelAccount      `json:"from"`
	Recipient    ChannelAccount      `json:"recipient"`
	Conversation ConversationAccount `json:"conversation"`
	Text         string              `json:"text"`
	TextFormat   string              `json:"textFormat"`
	ReplyToID    string              `json:"replyToId"`
	Attachments  []Attachment        `json:"attachments"`
	Entities     []Entity            `json:"entities"`
	ChannelData  MSTeamsChannelData  `json:"channelData"`
	MembersAdded []ChannelAccount    `json:"membersAdded"`
	Locale       string              `json:"locale"`
	Value        json.RawMessage     `json:"value,omitempty"` // used for invoke payloads
}

// ChannelAccount is a user or bot account reference.
type ChannelAccount struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AADObjectID  string `json:"aadObjectId,omitempty"`
	Role         string `json:"role,omitempty"`
}

// ConversationAccount describes the conversation.
type ConversationAccount struct {
	ID               string `json:"id"`
	IsGroup          bool   `json:"isGroup"`
	ConversationType string `json:"conversationType"` // personal | groupChat | channel
	TenantID         string `json:"tenantId,omitempty"`
	Name             string `json:"name,omitempty"`
}

// Attachment is a message attachment.
type Attachment struct {
	ContentType string          `json:"contentType"`
	ContentURL  string          `json:"contentUrl,omitempty"`
	Content     json.RawMessage `json:"content,omitempty"`
	Name        string          `json:"name,omitempty"`
}

// AttachmentContentFileDownloadInfo is the content of a
// application/vnd.microsoft.teams.file.download.info attachment.
type AttachmentContentFileDownloadInfo struct {
	DownloadURL string `json:"downloadUrl"`
	UniqueID    string `json:"uniqueId"`
	FileType    string `json:"fileType"`
	Etag        string `json:"etag,omitempty"`
}

// Entity is an entity in the message (e.g., a mention).
type Entity struct {
	Type      string          `json:"type"`
	Mentioned ChannelAccount  `json:"mentioned,omitempty"`
	Text      string          `json:"text,omitempty"`
}

// MSTeamsChannelData carries Teams-specific metadata.
type MSTeamsChannelData struct {
	Tenant  TenantInfo  `json:"tenant"`
	Channel ChannelInfo `json:"channel,omitempty"`
	Team    TeamInfo    `json:"team,omitempty"`
}

// TenantInfo is the AAD tenant.
type TenantInfo struct {
	ID string `json:"id"`
}

// ChannelInfo is the Teams channel (for channel-scope activities).
type ChannelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// TeamInfo is the Teams team.
type TeamInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// InvokeFileConsentAccept is the value payload when a user accepts a FileConsentCard.
type InvokeFileConsentAccept struct {
	Action     string               `json:"action"` // "accept" | "decline"
	UploadInfo FileConsentUploadInfo `json:"uploadInfo"`
}

// FileConsentUploadInfo carries the upload URL and metadata returned by Teams.
type FileConsentUploadInfo struct {
	ContentURL  string `json:"contentUrl"`
	UniqueID    string `json:"uniqueId"`
	FileType    string `json:"fileType"`
	Etag        string `json:"etag"`
	UploadURL   string `json:"uploadUrl"`
}
