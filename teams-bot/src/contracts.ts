export const SCHEMA_VERSION = 1;
export const INPUT_BYTE_LIMIT = 128 * 1024;
export const DRAFT_BYTE_LIMIT = 16 * 1024;
export const WEBHOOK_RESPONSE_BYTE_LIMIT = 32 * 1024;
export const TEAMS_TEXT_LIMIT = 20_000;
export const DEFAULT_GATEWAY_TIMEOUT_SEC = 600;
export const DEFAULT_HTTP_DEADLINE_SEC = 630;

export const JOB_STATES = [
  "queued",
  "reading",
  "generating",
  "ready_to_send",
  "sending",
  "sent",
  "failed",
  "delivery_unknown",
] as const;

export type JobState = (typeof JOB_STATES)[number];
export type JobOrigin = "teams" | "app";

export type ActorConfig = {
  id: string;
  mantomanUserId: string;
  entraObjectId: string;
  displayName: string;
};

export type RecipientConfig = {
  alias: string;
  entraObjectId: string;
  teamsUserId: string;
  displayName: string;
};

export type ProjectConfig = {
  id: string;
  aliases: string[];
  agentId: string;
  webhookId: string;
  webhookSecret: string;
  siteId: string;
  driveId: string;
  libraryPathPrefix: string;
  allowedHosts: string[];
  actors: string[];
  destination: { teamId: string; channelId: string };
  recipients: RecipientConfig[];
};

export type ServiceConfig = {
  installationId: string;
  microsoftTenantId: string;
  mantomanTenantId: string;
  mantomanBaseUrl: string;
  clientId: string;
  clientSecret: string;
  teamsServiceUrl: string;
  statePath: string;
  publicPort: number;
  privatePort: number;
  gatewaySyncTimeoutSec: number;
  httpDeadlineSec: number;
  mcpAllowedOrigins: string[];
  mcpAllowedHosts: string[];
  actors: ActorConfig[];
  projects: ProjectConfig[];
};

export type FileReference = {
  kind: "sharepoint-url" | "drive-item";
  url?: string;
  driveId?: string;
  itemId?: string;
};

export type NormalizedRequest = {
  actorId: string;
  projectId: string;
  file?: FileReference;
  recipientAlias?: string;
  recipientId?: string;
  destinationChannelId: string;
  text: string;
};

export type AssignmentDraft = {
  title: string;
  summary: string;
  acceptanceCriteria: string[];
  sourceReferences: string[];
};

export type DeliveryReceipt = {
  operation_id: string;
  status: "queued" | "sending" | "sent" | "failed" | "delivery_unknown";
  message_id: string | null;
  message_url: string | null;
  error_code: string | null;
};

export type JobRecord = {
  id: string;
  origin: JobOrigin;
  tenantId: string;
  installationId: string;
  conversationId: string;
  activityId: string | null;
  actorId: string;
  projectId: string;
  operationKey: string | null;
  requestHash: string;
  state: JobState;
  sessionKey: string;
  syntheticUserId: string;
  inputText: string | null;
  generatedText: string | null;
  draftJson: string | null;
  teamsMessageId: string | null;
  messageUrl: string | null;
  errorCode: string | null;
  errorDetail: string | null;
  callId: string | null;
  webhookBody: string | null;
  idempotencyKey: string | null;
  webhookDispatched: boolean;
  operatorReview: boolean;
  payloadJson: string;
  createdAt: string;
  updatedAt: string;
};

export type RouteRecord = {
  tenantId: string;
  teamId: string;
  channelId: string;
  conversationId: string;
  serviceUrl: string;
  rootReplyChainId: string | null;
  canPostRoot: boolean;
};

export type InboundActivity = {
  type: string;
  id: string;
  text: string;
  serviceUrl: string;
  replyToId?: string;
  conversation: { id: string; conversationType?: string; tenantId?: string };
  channelData: {
    tenant?: { id?: string };
    team?: { id?: string };
    channel?: { id?: string };
    teamsChannelId?: string;
    replyChain?: { id?: string };
  };
  from: { id?: string; name?: string; aadObjectId?: string; role?: string };
  recipient?: { id?: string; name?: string };
  entities: MentionEntity[];
  attachments: ActivityAttachment[];
};

export type MentionEntity = {
  type?: string;
  text?: string;
  mentioned?: { id?: string; name?: string; aadObjectId?: string; role?: string };
};

export type ActivityAttachment = {
  contentType?: string;
  contentUrl?: string;
  name?: string;
};

export type PolicyDenial = {
  ok: false;
  code:
    | "unknown_tenant"
    | "unknown_actor"
    | "unknown_project"
    | "unknown_destination"
    | "unknown_recipient"
    | "ambiguous_recipient"
    | "file_outside_scope"
    | "actor_not_permitted"
    | "credential_revoked"
    | "empty_identity";
  detail: string;
};

export type PolicyOk<T> = { ok: true; value: T };
export type PolicyResult<T> = PolicyOk<T> | PolicyDenial;
