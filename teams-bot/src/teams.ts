import { MessageActivity } from "@microsoft/teams.api";
import type { AssignmentDraft, FileReference, InboundActivity, MentionEntity, ProjectConfig, ServiceConfig } from "./contracts.js";
import { TEAMS_TEXT_LIMIT } from "./contracts.js";
import { authorizeActorProject, authorizeDestination, findActorByEntra, findProjectByChannel, resolveRecipient } from "./policy.js";
export type InboundDecision =
  | { kind: "ignore"; code: string; detail: string }
  | { kind: "reject"; code: string; detail: string }
  | { kind: "clarify"; text: string; projectId: string; actorId: string; channelId: string; conversationId: string; rootThreadId: string; canPostRoot: boolean; serviceUrl: string; teamId: string }
  | { kind: "question" | "assignment"; text: string; projectId: string; actorId: string; channelId: string; conversationId: string; rootThreadId: string; canPostRoot: boolean; serviceUrl: string; teamId: string; file?: FileReference; recipientId?: string };

const ASSIGNMENT_HELP = "Assignment command: assign recipient:<alias> file:<https://tenant.sharepoint.com/.../file.docx>. A configured person mention also counts as the recipient. Ambiguous commands are not posted.";

export function normalizeActivity(value: unknown): InboundActivity | null {
  if (!value || typeof value !== "object") return null;
  const activity = value as Record<string, unknown>;
  const conversation = asRecord(activity.conversation);
  const from = asRecord(activity.from);
  if (!conversation || typeof activity.id !== "string" || typeof activity.type !== "string") return null;
  return {
    type: activity.type,
    id: activity.id,
    text: typeof activity.text === "string" ? activity.text : "",
    serviceUrl: typeof activity.serviceUrl === "string" ? activity.serviceUrl : "",
    ...(typeof activity.replyToId === "string" ? { replyToId: activity.replyToId } : {}),
    conversation: {
      id: typeof conversation.id === "string" ? conversation.id : "",
      ...(typeof conversation.conversationType === "string" ? { conversationType: conversation.conversationType } : {}),
      ...(typeof conversation.tenantId === "string" ? { tenantId: conversation.tenantId } : {}),
    },
    channelData: asRecord(activity.channelData) as InboundActivity["channelData"] ?? {},
    from: {
      ...(typeof from?.id === "string" ? { id: from.id } : {}),
      ...(typeof from?.name === "string" ? { name: from.name } : {}),
      ...(typeof from?.aadObjectId === "string" ? { aadObjectId: from.aadObjectId } : {}),
      ...(typeof from?.role === "string" ? { role: from.role } : {}),
    },
    ...(asRecord(activity.recipient) ? { recipient: asRecord(activity.recipient) as InboundActivity["recipient"] } : {}),
    entities: Array.isArray(activity.entities) ? activity.entities as MentionEntity[] : [],
    attachments: Array.isArray(activity.attachments) ? activity.attachments as InboundActivity["attachments"] : [],
  };
}

export function classifyActivity(config: ServiceConfig, activity: InboundActivity): InboundDecision {
  if (activity.type !== "message") return { kind: "ignore", code: "not_message", detail: "only message activities start work" };
  const tenantId = activity.channelData.tenant?.id ?? activity.conversation.tenantId ?? "";
  if (!tenantId || tenantId !== config.microsoftTenantId) return { kind: "reject", code: "foreign_tenant", detail: "activity tenant does not match this installation" };
  if (!activity.serviceUrl || !sameOrigin(activity.serviceUrl, config.teamsServiceUrl)) {
    return { kind: "reject", code: "untrusted_service_url", detail: "activity service URL does not match the configured Teams service" };
  }
  if (!activity.from.aadObjectId || activity.from.role === "bot") return { kind: "reject", code: activity.from.aadObjectId ? "non_user" : "missing_identity", detail: "only directory users can start work" };
  if (!isBotMentioned(activity)) return { kind: "reject", code: "not_mentioned", detail: "channel messages must mention the bot" };
  const channelId = activity.channelData.channel?.id ?? activity.channelData.teamsChannelId ?? "";
  const teamId = activity.channelData.team?.id ?? "";
  if (!channelId || !teamId) return { kind: "reject", code: "unknown_destination", detail: "channel messages require a team and channel" };
  const actor = findActorByEntra(config, activity.from.aadObjectId);
  if (!actor) return { kind: "reject", code: "unknown_actor", detail: "sender is not a configured actor" };
  const project = findProjectByChannel(config, teamId, channelId);
  if (!project) return { kind: "reject", code: "unknown_destination", detail: "channel is not an approved project destination" };
  const allowed = authorizeActorProject(config, actor.id, project.id);
  if (!allowed.ok) return { kind: "reject", code: allowed.code, detail: allowed.detail };
  const destination = authorizeDestination(project, teamId, channelId);
  if (!destination.ok) return { kind: "reject", code: destination.code, detail: destination.detail };
  const existingThreadId = activity.channelData.replyChain?.id ?? activity.replyToId ?? "";
  const rootThreadId = existingThreadId || activity.id;
  const route = {
    projectId: project.id,
    actorId: actor.id,
    channelId,
    conversationId: activity.conversation.id,
    rootThreadId,
    canPostRoot: existingThreadId === "",
    serviceUrl: activity.serviceUrl,
    teamId,
  };
  const text = stripMentions(activity.text, activity.entities, activity.recipient?.id);
  const file = findFile(text, activity);
  const recipient = findRecipient(project, text, activity);
  if (recipient && !recipient.ok && recipient.code === "ambiguous_recipient") {
    return { kind: "clarify", text: "Recipient is ambiguous. Use a unique recipient:<alias> from the project directory.", ...route };
  }
  const assignment = /^assign\b/i.test(text);
  if (assignment) {
    if (!file || !recipient?.ok) return { kind: "clarify", text: ASSIGNMENT_HELP, ...route };
    return { kind: "assignment", text, file, recipientId: recipient.value.entraObjectId, ...route };
  }
  return { kind: "question", text, ...(file ? { file } : {}), ...route };
}

export function buildAssignmentActivity(draft: AssignmentDraft, recipient: { teamsUserId: string; entraObjectId: string; displayName: string }, sourceUrl: string): MessageActivity {
  const criteria = draft.acceptanceCriteria.map((item) => `- ${item}`).join("\n");
  const sources = draft.sourceReferences.map((item) => `- ${item}`).join("\n");
  const text = [
    draft.title,
    "",
    draft.summary,
    "",
    "Acceptance criteria:",
    criteria,
    "",
    "Sources:",
    sources,
    sourceUrl ? `File: ${sourceUrl}` : "",
    "",
    `Assignee: <at>${recipient.displayName}</at>`,
  ].filter((line) => line !== undefined).join("\n");
  if (Buffer.byteLength(text) > TEAMS_TEXT_LIMIT) {
    throw new Error("message_too_long");
  }
  const activity = new MessageActivity(text);
  activity.addMention({
    id: recipient.teamsUserId,
    name: recipient.displayName,
    aadObjectId: recipient.entraObjectId,
    role: "user",
  }, { addText: false, text: recipient.displayName });
  return activity;
}

export function buildPlainActivity(text: string): MessageActivity {
  if (Buffer.byteLength(text) > TEAMS_TEXT_LIMIT) throw new Error("message_too_long");
  return new MessageActivity(text);
}

export function messageLink(tenantId: string, channelId: string, messageId: string, parentMessageId?: string): string {
  const url = new URL(`https://teams.microsoft.com/l/message/${encodeURIComponent(channelId)}/${encodeURIComponent(messageId)}`);
  url.searchParams.set("tenantId", tenantId);
  if (parentMessageId) url.searchParams.set("parentMessageId", parentMessageId);
  return url.toString();
}

function isBotMentioned(activity: InboundActivity): boolean {
  const botId = activity.recipient?.id;
  return activity.entities.some((entity) => entity.type === "mention" && botId && entity.mentioned?.id === botId);
}

function stripMentions(text: string, entities: MentionEntity[], botId?: string): string {
  let next = text;
  for (const entity of entities) {
    if (entity.type === "mention" && entity.mentioned?.id === botId && entity.text) next = next.replace(entity.text, " ");
  }
  return next.replace(/\s+/g, " ").trim();
}

function findFile(text: string, activity: InboundActivity): FileReference | undefined {
  const drive = text.match(/drive\/([A-Za-z0-9_!:.-]+)\/item\/([A-Za-z0-9_!:.-]+)/);
  if (drive?.[1] && drive[2]) return { kind: "drive-item", driveId: drive[1], itemId: drive[2] };
  const url = text.match(/https:\/\/[^\s>]+\.sharepoint\.com\/[^\s>]+/i)?.[0];
  if (url) return { kind: "sharepoint-url", url };
  const attachment = activity.attachments.find((item) => item.contentUrl?.includes(".sharepoint.com"));
  if (attachment?.contentUrl) return { kind: "sharepoint-url", url: attachment.contentUrl };
  return undefined;
}

function findRecipient(project: ProjectConfig | undefined, text: string, activity: InboundActivity) {
  if (!project) return undefined;
  const alias = text.match(/recipient:([A-Za-z0-9_.:@-]+)/i)?.[1];
  const mention = activity.entities.find((entity) => entity.type === "mention" && entity.mentioned?.id && entity.mentioned.id !== activity.recipient?.id);
  return resolveRecipient(project, {
    ...(alias ? { alias } : {}),
    ...(mention?.mentioned?.aadObjectId ? { entraObjectId: mention.mentioned.aadObjectId } : {}),
    ...(mention?.mentioned?.id ? { teamsUserId: mention.mentioned.id } : {}),
  });
}

function sameOrigin(left: string, right: string): boolean {
  try {
    return new URL(left).origin === new URL(right).origin;
  } catch {
    return false;
  }
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  return value as Record<string, unknown>;
}
