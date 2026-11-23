import { closeSync, lstatSync, mkdirSync, openSync } from "node:fs";
import { dirname } from "node:path";
import { DatabaseSync } from "node:sqlite";
import type { JobRecord, JobState, RouteRecord } from "./contracts.js";
import { issueToken, newId, tokenHash } from "./ids.js";

export class StoreError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "StoreError";
  }
}

type JobRow = Record<string, string | number | null>;

export class OperationStore {
  private readonly db: DatabaseSync;

  constructor(path: string) {
    if (path !== ":memory:") {
      const directory = dirname(path);
      mkdirSync(directory, { recursive: true, mode: 0o700 });
      const directoryInfo = lstatSync(directory);
      if (!directoryInfo.isDirectory() || (directoryInfo.mode & 0o077) !== 0 || (process.getuid && directoryInfo.uid !== process.getuid())) {
        throw new StoreError("state directory must be owned by the service user and inaccessible to group and others");
      }
      try {
        closeSync(openSync(path, "wx", 0o600));
      } catch (error) {
        if (!error || typeof error !== "object" || !("code" in error) || error.code !== "EEXIST") throw error;
      }
      const databaseInfo = lstatSync(path);
      if (!databaseInfo.isFile() || (databaseInfo.mode & 0o077) !== 0 || (process.getuid && databaseInfo.uid !== process.getuid())) {
        throw new StoreError("state database must be a private regular file owned by the service user");
      }
    }
    this.db = new DatabaseSync(path);
    this.db.exec("PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;");
    this.migrate();
  }

  close(): void {
    this.db.close();
  }

  issueCredential(actorId: string): { id: string; token: string } {
    const token = issueToken();
    const id = newId();
    this.db.prepare(
      "INSERT INTO credentials (id, actor_id, secret_hash, created_at, revoked_at) VALUES (?, ?, ?, ?, NULL)",
    ).run(id, actorId, tokenHash(token), now());
    this.audit("credential.issue", { credentialId: id, actorId });
    return { id, token };
  }

  revokeCredential(id: string): void {
    const result = this.db.prepare("UPDATE credentials SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL").run(now(), id);
    if (Number(result.changes) !== 1) throw new StoreError("credential not found or already revoked");
    this.audit("credential.revoke", { credentialId: id });
  }

  actorForToken(token: string): { actorId: string; credentialId: string } | { error: "unknown" | "revoked" } {
    const row = this.db.prepare("SELECT id, actor_id, revoked_at FROM credentials WHERE secret_hash = ?").get(tokenHash(token)) as
      | { id: string; actor_id: string; revoked_at: string | null }
      | undefined;
    if (!row) return { error: "unknown" };
    if (row.revoked_at) return { error: "revoked" };
    return { actorId: row.actor_id, credentialId: row.id };
  }

  saveRoute(route: RouteRecord): void {
    this.db.prepare(
      `INSERT INTO routes (id, tenant_id, team_id, channel_id, conversation_id, service_url, root_reply_chain_id, can_post_root, registered_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(tenant_id, channel_id) DO UPDATE SET
         team_id = excluded.team_id,
         conversation_id = CASE WHEN routes.can_post_root = 1 AND excluded.can_post_root = 0 THEN routes.conversation_id ELSE excluded.conversation_id END,
         service_url = excluded.service_url,
         root_reply_chain_id = COALESCE(excluded.root_reply_chain_id, routes.root_reply_chain_id),
         can_post_root = CASE WHEN routes.can_post_root = 1 OR excluded.can_post_root = 1 THEN 1 ELSE 0 END`,
    ).run(
      newId(),
      route.tenantId,
      route.teamId,
      route.channelId,
      route.conversationId,
      route.serviceUrl,
      route.rootReplyChainId,
      route.canPostRoot ? 1 : 0,
      now(),
    );
  }

  getRoute(tenantId: string, channelId: string): RouteRecord | null {
    const row = this.db.prepare(
      "SELECT tenant_id, team_id, channel_id, conversation_id, service_url, root_reply_chain_id, can_post_root FROM routes WHERE tenant_id = ? AND channel_id = ?",
    ).get(tenantId, channelId) as JobRow | undefined;
    if (!row) return null;
    return {
      tenantId: String(row.tenant_id),
      teamId: String(row.team_id),
      channelId: String(row.channel_id),
      conversationId: String(row.conversation_id),
      serviceUrl: String(row.service_url),
      rootReplyChainId: row.root_reply_chain_id ? String(row.root_reply_chain_id) : null,
      canPostRoot: row.can_post_root === 1,
    };
  }

  insertJob(job: JobRecord): { status: "created" | "duplicate"; job: JobRecord } | { status: "conflict"; job: JobRecord } {
    try {
      this.db.prepare(
        `INSERT INTO jobs (
          id, origin, tenant_id, installation_id, conversation_id, activity_id, actor_id, project_id, operation_key,
          request_hash, state, session_key, synthetic_user_id, input_text, generated_text, draft_json, teams_message_id,
          message_url, error_code, error_detail, call_id, webhook_body, idempotency_key, webhook_dispatched, operator_review,
          payload_json, created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      ).run(
        job.id, job.origin, job.tenantId, job.installationId, job.conversationId, job.activityId, job.actorId, job.projectId,
        job.operationKey, job.requestHash, job.state, job.sessionKey, job.syntheticUserId, job.inputText, job.generatedText,
        job.draftJson, job.teamsMessageId, job.messageUrl, job.errorCode, job.errorDetail, job.callId, job.webhookBody,
        job.idempotencyKey, job.webhookDispatched ? 1 : 0, job.operatorReview ? 1 : 0, job.payloadJson, job.createdAt, job.updatedAt,
      );
      return { status: "created", job };
    } catch (error) {
      const existing = job.activityId
        ? this.getByActivity(job.tenantId, job.conversationId, job.activityId)
        : job.operationKey
          ? this.getByOperation(job.actorId, job.operationKey)
          : null;
      if (!existing) throw error;
      if (existing.requestHash !== job.requestHash) return { status: "conflict", job: existing };
      return { status: "duplicate", job: existing };
    }
  }

  getJob(id: string): JobRecord | null {
    const row = this.db.prepare("SELECT * FROM jobs WHERE id = ?").get(id) as JobRow | undefined;
    return row ? mapJob(row) : null;
  }

  getByActivity(tenantId: string, conversationId: string, activityId: string): JobRecord | null {
    const row = this.db.prepare("SELECT * FROM jobs WHERE tenant_id = ? AND conversation_id = ? AND activity_id = ?").get(tenantId, conversationId, activityId) as JobRow | undefined;
    return row ? mapJob(row) : null;
  }

  getByOperation(actorId: string, operationKey: string): JobRecord | null {
    const row = this.db.prepare("SELECT * FROM jobs WHERE actor_id = ? AND operation_key = ?").get(actorId, operationKey) as JobRow | undefined;
    return row ? mapJob(row) : null;
  }

  listJobs(): JobRecord[] {
    const rows = this.db.prepare("SELECT * FROM jobs ORDER BY created_at").all() as JobRow[];
    return rows.map(mapJob);
  }

  transition(id: string, from: JobState | JobState[], to: JobState, patch: Partial<JobRecord> = {}, audit?: { action: string; detail: Record<string, unknown> }): JobRecord {
    const current = this.getJob(id);
    if (!current) throw new StoreError("job not found");
    const allowed = Array.isArray(from) ? from : [from];
    if (!allowed.includes(current.state)) throw new StoreError(`invalid transition ${current.state} -> ${to}`);
    const next: JobRecord = { ...current, ...patch, state: to, updatedAt: now() };
    this.db.exec("BEGIN IMMEDIATE");
    try {
      const changed = this.db.prepare(
        `UPDATE jobs SET state = ?, input_text = ?, generated_text = ?, draft_json = ?, teams_message_id = ?, message_url = ?,
         error_code = ?, error_detail = ?, call_id = ?, webhook_body = ?, idempotency_key = ?, webhook_dispatched = ?,
         operator_review = ?, payload_json = ?, updated_at = ? WHERE id = ? AND state = ?`,
      ).run(
        next.state, next.inputText, next.generatedText, next.draftJson, next.teamsMessageId, next.messageUrl, next.errorCode,
        next.errorDetail, next.callId, next.webhookBody, next.idempotencyKey, next.webhookDispatched ? 1 : 0,
        next.operatorReview ? 1 : 0, next.payloadJson, next.updatedAt, id, current.state,
      );
      if (Number(changed.changes) !== 1) throw new StoreError("lost state transition race");
      if (audit) this.audit(audit.action, audit.detail, id);
      this.db.exec("COMMIT");
    } catch (error) {
      this.db.exec("ROLLBACK");
      throw error;
    }
    return next;
  }

  recoverInterrupted(): { requeued: string[]; review: string[]; unknown: string[] } {
    const review: string[] = [];
    const unknown: string[] = [];
    const requeued: string[] = [];
    for (const job of this.listJobs()) {
      if (job.state === "reading" || job.state === "generating") {
        this.transition(job.id, job.state, "failed", {
          errorCode: job.state === "generating" ? "interrupted_generating" : "interrupted_reading",
          errorDetail: "interrupted before dispatch; operator review required",
          operatorReview: true,
        });
        review.push(job.id);
      } else if (job.state === "sending") {
        this.transition(job.id, "sending", "delivery_unknown", {
          errorCode: "interrupted_sending",
          errorDetail: "send was in progress at restart",
          operatorReview: true,
        });
        unknown.push(job.id);
      } else if (job.state === "queued" || job.state === "ready_to_send") {
        requeued.push(job.id);
      }
    }
    this.audit("store.recover", { requeued, review, unknown });
    return { requeued, review, unknown };
  }

  takeForWork(): JobRecord | null {
    this.db.exec("BEGIN IMMEDIATE");
    try {
      const row = this.db.prepare(
        "SELECT * FROM jobs WHERE state IN ('queued', 'ready_to_send') ORDER BY CASE WHEN state = 'ready_to_send' THEN 0 ELSE 1 END, updated_at, created_at LIMIT 1",
      ).get() as JobRow | undefined;
      if (!row) {
        this.db.exec("COMMIT");
        return null;
      }
      const job = mapJob(row);
      const next: JobRecord["state"] = job.state === "queued" ? "reading" : "sending";
      this.db.prepare("UPDATE jobs SET state = ?, updated_at = ? WHERE id = ? AND state = ?").run(next, now(), job.id, job.state);
      this.db.exec("COMMIT");
      return { ...job, state: next, updatedAt: now() };
    } catch (error) {
      this.db.exec("ROLLBACK");
      throw error;
    }
  }

  recordAttempt(jobId: string, outcome: string, messageId: string | null, detail: string): void {
    this.db.prepare(
      "INSERT INTO delivery_attempts (id, job_id, started_at, finished_at, outcome, message_id, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
    ).run(newId(), jobId, now(), now(), outcome, messageId, detail);
  }

  hasDeliveryAttempt(jobId: string): boolean {
    return this.db.prepare("SELECT 1 FROM delivery_attempts WHERE job_id = ? LIMIT 1").get(jobId) !== undefined;
  }

  audit(action: string, detail: Record<string, unknown>, jobId?: string): void {
    this.db.prepare("INSERT INTO audit (id, at, actor, action, job_id, detail) VALUES (?, ?, 'operator', ?, ?, ?)").run(
      newId(),
      now(),
      action,
      jobId ?? null,
      JSON.stringify(detail),
    );
  }

  auditsFor(jobId: string): Array<{ action: string; detail: string }> {
    return this.db.prepare("SELECT action, detail FROM audit WHERE job_id = ? OR detail LIKE ? ORDER BY at").all(jobId, `%${jobId}%`) as Array<{ action: string; detail: string }>;
  }

  private migrate(): void {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL);
      CREATE TABLE IF NOT EXISTS credentials (
        id TEXT PRIMARY KEY,
        actor_id TEXT NOT NULL,
        secret_hash TEXT NOT NULL UNIQUE,
        created_at TEXT NOT NULL,
        revoked_at TEXT
      );
      CREATE TABLE IF NOT EXISTS routes (
        id TEXT PRIMARY KEY,
        tenant_id TEXT NOT NULL,
        team_id TEXT NOT NULL,
        channel_id TEXT NOT NULL,
        conversation_id TEXT NOT NULL,
        service_url TEXT NOT NULL,
        root_reply_chain_id TEXT,
        can_post_root INTEGER NOT NULL,
        registered_at TEXT NOT NULL,
        UNIQUE(tenant_id, channel_id)
      );
      CREATE TABLE IF NOT EXISTS jobs (
        id TEXT PRIMARY KEY,
        origin TEXT NOT NULL,
        tenant_id TEXT NOT NULL,
        installation_id TEXT NOT NULL,
        conversation_id TEXT NOT NULL,
        activity_id TEXT,
        actor_id TEXT NOT NULL,
        project_id TEXT NOT NULL,
        operation_key TEXT,
        request_hash TEXT NOT NULL,
        state TEXT NOT NULL,
        session_key TEXT NOT NULL,
        synthetic_user_id TEXT NOT NULL,
        input_text TEXT,
        generated_text TEXT,
        draft_json TEXT,
        teams_message_id TEXT,
        message_url TEXT,
        error_code TEXT,
        error_detail TEXT,
        call_id TEXT,
        webhook_body TEXT,
        idempotency_key TEXT,
        webhook_dispatched INTEGER NOT NULL,
        operator_review INTEGER NOT NULL,
        payload_json TEXT NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
      );
      CREATE UNIQUE INDEX IF NOT EXISTS jobs_activity ON jobs(tenant_id, conversation_id, activity_id) WHERE activity_id IS NOT NULL;
      CREATE UNIQUE INDEX IF NOT EXISTS jobs_operation ON jobs(actor_id, operation_key) WHERE operation_key IS NOT NULL;
      CREATE TABLE IF NOT EXISTS delivery_attempts (
        id TEXT PRIMARY KEY,
        job_id TEXT NOT NULL,
        started_at TEXT NOT NULL,
        finished_at TEXT,
        outcome TEXT NOT NULL,
        message_id TEXT,
        detail TEXT
      );
      CREATE TABLE IF NOT EXISTS audit (
        id TEXT PRIMARY KEY,
        at TEXT NOT NULL,
        actor TEXT NOT NULL,
        action TEXT NOT NULL,
        job_id TEXT,
        detail TEXT NOT NULL
      );
    `);
    const version = this.db.prepare("SELECT version FROM schema_meta").get() as { version: number } | undefined;
    if (!version) this.db.prepare("INSERT INTO schema_meta (version) VALUES (1)").run();
  }
}

function now(): string {
  return new Date().toISOString();
}

function mapJob(row: JobRow): JobRecord {
  return {
    id: String(row.id),
    origin: row.origin === "app" ? "app" : "teams",
    tenantId: String(row.tenant_id),
    installationId: String(row.installation_id),
    conversationId: String(row.conversation_id),
    activityId: row.activity_id ? String(row.activity_id) : null,
    actorId: String(row.actor_id),
    projectId: String(row.project_id),
    operationKey: row.operation_key ? String(row.operation_key) : null,
    requestHash: String(row.request_hash),
    state: String(row.state) as JobRecord["state"],
    sessionKey: String(row.session_key),
    syntheticUserId: String(row.synthetic_user_id),
    inputText: row.input_text ? String(row.input_text) : null,
    generatedText: row.generated_text ? String(row.generated_text) : null,
    draftJson: row.draft_json ? String(row.draft_json) : null,
    teamsMessageId: row.teams_message_id ? String(row.teams_message_id) : null,
    messageUrl: row.message_url ? String(row.message_url) : null,
    errorCode: row.error_code ? String(row.error_code) : null,
    errorDetail: row.error_detail ? String(row.error_detail) : null,
    callId: row.call_id ? String(row.call_id) : null,
    webhookBody: row.webhook_body ? String(row.webhook_body) : null,
    idempotencyKey: row.idempotency_key ? String(row.idempotency_key) : null,
    webhookDispatched: row.webhook_dispatched === 1,
    operatorReview: row.operator_review === 1,
    payloadJson: String(row.payload_json),
    createdAt: String(row.created_at),
    updatedAt: String(row.updated_at),
  };
}
