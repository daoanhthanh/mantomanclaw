import type { AssignmentDraft, JobRecord, ServiceConfig } from "./contracts.js";
import { canonicalJson, newId, sessionKey, sha256Hex, syntheticUserId } from "./ids.js";
import { buildInput, invokeWebhook, parseDraft, type WebhookResult } from "./mantoman.js";
import { findProject, resolveRecipient } from "./policy.js";
import type { OperationStore } from "./store.js";
import { buildAssignmentActivity, buildPlainActivity, messageLink } from "./teams.js";
import { logEvent } from "./redact.js";
import type { MessageActivity } from "@microsoft/teams.api";

export type TeamsSender = {
  sendRoot(conversationId: string, activity: MessageActivity): Promise<{ id: string }>;
  sendThread(channelOrChatId: string, rootMessageId: string, activity: MessageActivity): Promise<{ id: string }>;
};

export type FileReader = {
  read(projectId: string, payload: JobPayload): Promise<{ name: string; text: string; coverage: string; webUrl: string }>;
};

export type JobPayload = {
  kind: "assignment" | "question" | "clarification" | "app_assignment";
  text: string;
  file?: { kind: "sharepoint-url" | "drive-item"; url?: string; driveId?: string; itemId?: string };
  recipientId?: string;
  channelId: string;
  rootThreadId: string;
  draft?: AssignmentDraft;
  sourceUrl?: string;
};

export async function processAvailable(config: ServiceConfig, store: OperationStore, sender: TeamsSender, reader: FileReader, fetchImpl?: typeof fetch): Promise<boolean> {
  const job = store.takeForWork();
  if (!job) return false;
  if (job.state === "reading") {
    await runGeneration(config, store, job, reader, fetchImpl);
    const ready = store.getJob(job.id);
    if (ready?.state === "ready_to_send") {
      store.transition(ready.id, "ready_to_send", "sending");
      await runSend(config, store, { ...ready, state: "sending" }, sender);
    } else if (ready?.state === "failed" && ready.origin === "teams") {
      enqueueFailureNotice(store, ready);
    }
  } else if (job.state === "sending") {
    await runSend(config, store, job, sender);
  }
  return true;
}

async function runGeneration(config: ServiceConfig, store: OperationStore, job: JobRecord, reader: FileReader, fetchImpl?: typeof fetch): Promise<void> {
  const payload = JSON.parse(job.payloadJson) as JobPayload;
  if (payload.kind === "clarification" || payload.kind === "app_assignment") {
    store.transition(job.id, "reading", "ready_to_send", {
      generatedText: payload.kind === "clarification" ? payload.text : JSON.stringify(payload.draft),
      draftJson: payload.draft ? JSON.stringify(payload.draft) : null,
    });
    return;
  }
  let labelled: { name: string; text: string; coverage: string; webUrl: string } | null = null;
  if (payload.file) {
    try {
      labelled = await reader.read(job.projectId, payload);
    } catch (error) {
      store.transition(job.id, "reading", "failed", { errorCode: "document_rejected", errorDetail: error instanceof Error ? error.message : "read failed", operatorReview: false });
      return;
    }
  }
  let input: { text: string; coverage: string };
  try {
    input = buildInput({
      instruction: `${payload.kind === "assignment"
        ? "The user requested an assignment. Reply with JSON only: {\"title\":\"\",\"summary\":\"\",\"acceptanceCriteria\":[],\"sourceReferences\":[]}. Do not invent deadlines."
        : "Answer the user in plain text."}\nUser request: ${payload.text}`,
      documents: labelled ? [{ label: labelled.name, text: labelled.text, coverage: labelled.coverage, required: payload.kind === "assignment" }] : [],
    });
  } catch {
    store.transition(job.id, "reading", "failed", { errorCode: "input_too_large", errorDetail: "required document did not fit in the input cap", operatorReview: false });
    return;
  }
  const body = JSON.stringify({
    input: input.text,
    session_key: job.sessionKey,
    user_id: job.syntheticUserId,
    mode: "sync",
  });
  const idempotencyKey = job.idempotencyKey ?? `teams:${job.id}`;
  store.transition(job.id, "reading", "generating", {
    inputText: input.text,
    webhookBody: body,
    idempotencyKey,
    webhookDispatched: true,
    payloadJson: canonicalJson(labelled ? { ...payload, sourceUrl: labelled.webUrl } : payload),
  });
  const project = findProject(config, job.projectId);
  if (!project) {
    store.transition(job.id, "generating", "failed", { errorCode: "unknown_project", operatorReview: true });
    return;
  }
  const result = await invokeWebhook(config, project, body, idempotencyKey, fetchImpl);
  if (result.kind === "output" && result.agentId !== project.agentId) {
    store.transition(job.id, "generating", "failed", {
      errorCode: "webhook_agent_mismatch",
      errorDetail: "webhook responded for a different project agent",
      operatorReview: true,
      webhookDispatched: true,
      callId: result.callId,
    });
    return;
  }
  applyWebhook(store, job.id, result, payload.kind === "assignment");
}

export function recoverFailureNotices(store: OperationStore): void {
  for (const job of store.listJobs()) {
    if (job.origin === "teams" && job.state === "failed" && job.activityId && !job.activityId.endsWith(":failure")) {
      enqueueFailureNotice(store, job);
    }
  }
}

function enqueueFailureNotice(store: OperationStore, job: JobRecord): void {
  const original = JSON.parse(job.payloadJson) as JobPayload;
  const text = `Unable to complete this request. An operator can inspect operation ${job.id}.`;
  const notice: JobPayload = { kind: "clarification", text, channelId: original.channelId, rootThreadId: original.rootThreadId };
  const timestamp = new Date().toISOString();
  store.insertJob({
    ...job,
    id: newId(),
    activityId: `${job.activityId}:failure`,
    requestHash: sha256Hex(text),
    state: "queued",
    inputText: null,
    generatedText: null,
    draftJson: null,
    teamsMessageId: null,
    messageUrl: null,
    errorCode: null,
    errorDetail: null,
    callId: null,
    webhookBody: null,
    idempotencyKey: null,
    webhookDispatched: false,
    operatorReview: false,
    payloadJson: canonicalJson(notice),
    createdAt: timestamp,
    updatedAt: timestamp,
  });
}

function applyWebhook(store: OperationStore, jobId: string, result: WebhookResult, assignment: boolean): void {
  if (result.kind === "unavailable") {
    store.transition(jobId, "generating", "queued", { errorCode: "gateway_unavailable", errorDetail: "gateway connection was unavailable before dispatch", webhookDispatched: false });
    return;
  }
  if (result.kind === "timeout") {
    store.transition(jobId, "generating", "failed", { errorCode: "webhook_timeout", errorDetail: "webhook timed out after dispatch; no automatic replay", operatorReview: true, webhookDispatched: true });
    return;
  }
  if (result.kind === "pending") {
    store.transition(jobId, "generating", "failed", { errorCode: "webhook_pending", errorDetail: "webhook returned pending; operator reconciliation required", operatorReview: true, webhookDispatched: true, callId: result.callId });
    return;
  }
  if (result.kind === "failure") {
    store.transition(jobId, "generating", "failed", { errorCode: result.code, errorDetail: "webhook envelope was not deliverable", operatorReview: true, webhookDispatched: true, callId: result.callId });
    return;
  }
  if (assignment) {
    try {
      const draft = parseDraft(result.output);
      store.transition(jobId, "generating", "ready_to_send", { generatedText: result.output, draftJson: JSON.stringify(draft), callId: result.callId, webhookDispatched: true });
      return;
    } catch {
      store.transition(jobId, "generating", "failed", { errorCode: "invalid_draft", errorDetail: "assignment output was not a valid draft", operatorReview: true, webhookDispatched: true, callId: result.callId });
      return;
    }
  }
  store.transition(jobId, "generating", "ready_to_send", { generatedText: result.output, callId: result.callId, webhookDispatched: true });
}

async function runSend(config: ServiceConfig, store: OperationStore, job: JobRecord, sender: TeamsSender): Promise<void> {
  const payload = JSON.parse(job.payloadJson) as JobPayload;
  const project = findProject(config, job.projectId);
  if (!project) {
    store.transition(job.id, "sending", "failed", { errorCode: "unknown_project", errorDetail: "project disappeared before send" });
    return;
  }
  const route = store.getRoute(config.microsoftTenantId, payload.channelId);
  if (!route) {
    store.transition(job.id, "sending", "failed", { errorCode: "unregistered_destination", errorDetail: "destination has no verified bot mention" });
    return;
  }
  let activity;
  try {
    if (payload.kind === "assignment" || payload.kind === "app_assignment") {
      const draft = JSON.parse(job.draftJson ?? "") as AssignmentDraft;
      const recipient = resolveRecipient(project, { entraObjectId: payload.recipientId });
      if (!recipient.ok) {
        store.transition(job.id, "sending", "failed", { errorCode: recipient.code, errorDetail: recipient.detail });
        return;
      }
      activity = buildAssignmentActivity(draft, recipient.value, payload.sourceUrl ?? "");
    } else {
      activity = buildPlainActivity(job.generatedText ?? payload.text);
    }
  } catch (error) {
    store.transition(job.id, "sending", "failed", { errorCode: error instanceof Error ? error.message : "message_rejected", errorDetail: "message was not sent" });
    return;
  }
  store.recordAttempt(job.id, "started", null, "send started");
  try {
    const sent = payload.rootThreadId
      ? await sender.sendThread(route.channelId, payload.rootThreadId, activity)
      : await sender.sendRoot(route.conversationId, activity);
    if (!sent.id) throw new Error("missing message id");
    const url = messageLink(config.microsoftTenantId, route.channelId, sent.id, payload.rootThreadId || undefined);
    store.transition(job.id, "sending", "sent", { teamsMessageId: sent.id, messageUrl: url, errorCode: null, errorDetail: null });
    store.recordAttempt(job.id, "sent", sent.id, "teams accepted the message");
  } catch (error) {
    const detail = error instanceof Error ? error.message : "send failed";
    store.transition(job.id, "sending", "delivery_unknown", { errorCode: "delivery_unknown", errorDetail: detail, operatorReview: true });
    store.recordAttempt(job.id, "delivery_unknown", null, detail);
    logEvent("warn", "teams.send_uncertain", { jobId: job.id });
  }
}

export function jobForTeams(config: ServiceConfig, input: {
  id: string;
  activityId: string;
  actorId: string;
  projectId: string;
  conversationId: string;
  channelId: string;
  rootThreadId: string;
  text: string;
  kind: JobPayload["kind"];
  file?: JobPayload["file"];
  recipientId?: string;
  requestHash: string;
}): JobRecord {
  const project = findProject(config, input.projectId);
  if (!project) throw new Error("unknown_project");
  const stamp = new Date().toISOString();
  const payload: JobPayload = {
    kind: input.kind,
    text: input.text,
    channelId: input.channelId,
    rootThreadId: input.rootThreadId,
    ...(input.file ? { file: input.file } : {}),
    ...(input.recipientId ? { recipientId: input.recipientId } : {}),
  };
  return {
    id: input.id,
    origin: "teams",
    tenantId: config.microsoftTenantId,
    installationId: config.installationId,
    conversationId: input.conversationId,
    activityId: input.activityId,
    actorId: input.actorId,
    projectId: project.id,
    operationKey: null,
    requestHash: input.requestHash,
    state: "queued",
    sessionKey: sessionKey({
      installationId: config.installationId,
      agentId: project.agentId,
      projectId: project.id,
      actorId: input.actorId,
      conversationId: input.conversationId,
      rootThreadId: input.rootThreadId,
    }),
    syntheticUserId: syntheticUserId({ installationId: config.installationId, projectId: project.id, actorId: input.actorId }),
    inputText: null,
    generatedText: null,
    draftJson: null,
    teamsMessageId: null,
    messageUrl: null,
    errorCode: null,
    errorDetail: null,
    callId: null,
    webhookBody: null,
    idempotencyKey: `teams:${config.installationId}:${input.activityId}`,
    webhookDispatched: false,
    operatorReview: false,
    payloadJson: canonicalJson(payload),
    createdAt: stamp,
    updatedAt: stamp,
  };
}
