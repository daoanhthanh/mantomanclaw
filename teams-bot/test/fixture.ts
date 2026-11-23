import type { InboundActivity, ServiceConfig } from "../src/contracts.js";
import { OperationStore } from "../src/store.js";

export function testConfig(overrides: Partial<ServiceConfig> = {}): ServiceConfig {
  return {
    installationId: "install-1",
    microsoftTenantId: "tenant-ms",
    mantomanTenantId: "tenant-mm",
    mantomanBaseUrl: "http://127.0.0.1:9",
    clientId: "client-1",
    clientSecret: "client-secret-value",
    teamsServiceUrl: "https://smba.trafficmanager.net/teams",
    statePath: ":memory:",
    publicPort: 3978,
    privatePort: 3979,
    gatewaySyncTimeoutSec: 600,
    httpDeadlineSec: 630,
    mcpAllowedOrigins: [],
    mcpAllowedHosts: ["127.0.0.1", "localhost", "teams-bot"],
    actors: [
      { id: "actor-a", mantomanUserId: "user-a", entraObjectId: "entra-a", displayName: "Ada" },
      { id: "actor-b", mantomanUserId: "user-b", entraObjectId: "entra-b", displayName: "Bea" },
    ],
    projects: [
      {
        id: "phoenix",
        aliases: ["phoenix"],
        agentId: "agent-phoenix",
        webhookId: "webhook-phoenix",
        webhookSecret: "webhook-secret-value",
        siteId: "site-phoenix",
        driveId: "drive-phoenix",
        libraryPathPrefix: "Shared Documents",
        allowedHosts: ["contoso.sharepoint.com"],
        actors: ["actor-a"],
        destination: { teamId: "team-phoenix", channelId: "channel-phoenix" },
        recipients: [
          { alias: "alice", entraObjectId: "entra-alice", teamsUserId: "29:alice", displayName: "Alice" },
          { alias: "amos", entraObjectId: "entra-amos", teamsUserId: "29:amos", displayName: "Amos" },
        ],
      },
      {
        id: "orion",
        aliases: ["orion"],
        agentId: "agent-orion",
        siteId: "site-orion",
        webhookId: "webhook-orion",
        webhookSecret: "orion-secret-value",
        driveId: "drive-orion",
        libraryPathPrefix: "Requirements",
        allowedHosts: ["contoso.sharepoint.com"],
        actors: ["actor-b"],
        destination: { teamId: "team-orion", channelId: "channel-orion" },
        recipients: [
          { alias: "alice", entraObjectId: "entra-alice", teamsUserId: "29:alice", displayName: "Alice" },
        ],
      },
    ],
    ...overrides,
  };
}

export function activity(overrides: Partial<InboundActivity> = {}): InboundActivity {
  return {
    type: "message",
    id: "activity-1",
    text: "<at>Mantoman</at> assign recipient:alice file:https://contoso.sharepoint.com/sites/phoenix/Shared%20Documents/req.docx",
    serviceUrl: "https://smba.trafficmanager.net/teams",
    conversation: { id: "19:channel-phoenix@thread.tacv2", conversationType: "channel", tenantId: "tenant-ms" },
    channelData: {
      tenant: { id: "tenant-ms" },
      team: { id: "team-phoenix" },
      channel: { id: "channel-phoenix" },
    },
    from: { id: "29:ada", name: "Ada", aadObjectId: "entra-a", role: "user" },
    recipient: { id: "28:bot", name: "Mantoman" },
    entities: [{ type: "mention", text: "<at>Mantoman</at>", mentioned: { id: "28:bot", name: "Mantoman" } }],
    attachments: [],
    ...overrides,
  };
}

export function tempStore(): OperationStore {
  return new OperationStore(":memory:");
}
