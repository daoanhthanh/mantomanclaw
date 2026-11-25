CREATE TABLE IF NOT EXISTS msteams_conversations (
    tenant_id        UUID NOT NULL REFERENCES tenants(id),
    channel_instance VARCHAR(255) NOT NULL,
    conversation_id  TEXT NOT NULL,
    kind             VARCHAR(16) NOT NULL,
    service_url      TEXT NOT NULL,
    ms_tenant_id     VARCHAR(64) NOT NULL,
    team_id          TEXT,
    teams_channel_id TEXT,
    user_aad_id      VARCHAR(64),
    title            TEXT,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, channel_instance, conversation_id)
);
CREATE INDEX IF NOT EXISTS idx_msteams_conv_user ON msteams_conversations(tenant_id, channel_instance, user_aad_id) WHERE user_aad_id IS NOT NULL;
