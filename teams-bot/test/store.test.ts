import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { jobForTeams } from "../src/worker.js";
import { OperationStore } from "../src/store.js";
import { sha256Hex } from "../src/ids.js";
import { testConfig } from "./fixture.js";

test("rejects an insecure state directory without changing its permissions", () => {
  const dir = mkdtempSync(join(tmpdir(), "teams-bot-permissions-"));
  chmodSync(dir, 0o755);
  assert.throws(() => new OperationStore(join(dir, "state.sqlite")), /state directory/);
  assert.equal(statSync(dir).mode & 0o777, 0o755);
});

test("credential revocation and conflicting idempotency bodies", () => {
  const store = new OperationStore(":memory:");
  const issued = store.issueCredential("actor-a");
  const resolved = store.actorForToken(issued.token);
  if ("error" in resolved) throw new Error("expected actor");
  assert.equal(resolved.actorId, "actor-a");
  store.revokeCredential(issued.id);
  assert.deepEqual(store.actorForToken(issued.token), { error: "revoked" });
  const config = testConfig();
  const first = jobForTeams(config, base("activity-1", "hash-1"));
  assert.equal(store.insertJob(first).status, "created");
  assert.equal(store.insertJob({ ...first, id: "other" }).status, "duplicate");
  const conflict = store.insertJob({ ...first, id: "third", requestHash: "hash-2" });
  assert.equal(conflict.status, "conflict");
  store.close();
});

test("duplicate activities survive restart and uncertain sends are not replayed", () => {
  const dir = mkdtempSync(join(tmpdir(), "teams-bot-"));
  const path = join(dir, "state.sqlite");
  const config = testConfig();
  const first = new OperationStore(path);
  const queued = jobForTeams(config, base("activity-9", sha256Hex("same")));
  first.insertJob(queued);
  const sending = jobForTeams(config, { ...base("activity-8", sha256Hex("send")), id: "job-send" });
  first.insertJob(sending);
  first.transition(sending.id, "queued", "reading");
  first.transition(sending.id, "reading", "generating");
  first.transition(sending.id, "generating", "ready_to_send");
  first.transition(sending.id, "ready_to_send", "sending");
  first.close();
  const reopened = new OperationStore(path);
  const duplicate = reopened.insertJob(jobForTeams(config, base("activity-9", sha256Hex("same"))));
  assert.equal(duplicate.status, "duplicate");
  assert.equal(reopened.listJobs().filter((job) => job.activityId === "activity-9").length, 1);
  const recovered = reopened.recoverInterrupted();
  assert.deepEqual(recovered.unknown, [sending.id]);
  reopened.recordAttempt(sending.id, "delivery_unknown", null, "operator must reconcile");
  assert.equal(reopened.hasDeliveryAttempt(sending.id), true);
  assert.equal(reopened.getJob(sending.id)?.state, "delivery_unknown");
  assert.equal(reopened.takeForWork()?.id, queued.id);
  assert.equal(reopened.takeForWork(), null);
  reopened.close();
});

function base(activityId: string, requestHash: string) {
  return {
    id: `job-${activityId}`,
    activityId,
    actorId: "actor-a",
    projectId: "phoenix",
    conversationId: "conversation-1",
    channelId: "channel-phoenix",
    rootThreadId: "",
    text: "assign recipient:alice",
    kind: "assignment" as const,
    requestHash,
  };
}
