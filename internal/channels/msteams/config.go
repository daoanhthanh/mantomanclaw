package msteams

import "github.com/nextlevelbuilder/goclaw/internal/config"

// msteamsCreds holds the encrypted credentials stored in channel_instances.credentials.
// Either ClientSecret OR (CertificatePEM + PrivateKeyPEM) must be set.
// Certificate credentials are required for file downloads from 2027-03-01.
type msteamsCreds struct {
	AppID          string `json:"app_id"`
	TenantID       string `json:"tenant_id"`
	ClientSecret   string `json:"client_secret,omitempty"`
	CertificatePEM string `json:"certificate_pem,omitempty"`
	PrivateKeyPEM  string `json:"private_key_pem,omitempty"`
}

// msteamsInstanceConfig holds non-secret config from channel_instances.config JSONB.
type msteamsInstanceConfig struct {
	DMPolicy       string                     `json:"dm_policy,omitempty"`
	GroupPolicy    string                     `json:"group_policy,omitempty"`
	AllowFrom      []string                   `json:"allow_from,omitempty"`
	RequireMention *bool                      `json:"require_mention,omitempty"`
	HistoryLimit   int                        `json:"history_limit,omitempty"`
	ThreadTTL      *int                       `json:"thread_ttl,omitempty"`
	ReactionLevel  string                     `json:"reaction_level,omitempty"`
	BlockReply     *bool                      `json:"block_reply,omitempty"`
	ChatBehavior   *config.ChatBehaviorConfig `json:"chat_behavior,omitempty"`
	DMStream       *bool                      `json:"dm_stream,omitempty"`
	GroupStream    *bool                      `json:"group_stream,omitempty"`
	// AllowedSiteIDs lists SharePoint site IDs the bot may read via Sites.Selected.
	AllowedSiteIDs []string `json:"allowed_site_ids,omitempty"`
	// AllowedHosts lists additional SharePoint hosts the bot may access.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
}
