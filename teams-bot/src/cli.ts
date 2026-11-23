import { loadConfig } from "./config.js";
import { findActor } from "./policy.js";
import { OperationStore } from "./store.js";
import type { JobState } from "./contracts.js";

function arg(name: string): string | undefined {
  const index = process.argv.indexOf(name);
  const value = index >= 0 ? process.argv[index + 1] : undefined;
  return value && !value.startsWith("--") ? value : undefined;
}

function main(): void {
  const config = loadConfig();
  const store = new OperationStore(config.statePath);
  const command = process.argv[2];
  const action = process.argv[3];
  try {
    if (command === "credentials" && action === "issue") {
      const actorId = arg("--actor");
      if (!actorId || !findActor(config, actorId)) throw new Error("unknown actor");
      const issued = store.issueCredential(actorId);
      process.stdout.write(`${issued.id} ${issued.token}\n`);
      return;
    }
    if (command === "credentials" && action === "revoke") {
      const id = arg("--id");
      if (!id) throw new Error("credential id required");
      store.revokeCredential(id);
      process.stdout.write("revoked\n");
      return;
    }
    if (command === "jobs" && action === "inspect") {
      const id = arg("--id");
      const jobs = id ? [store.getJob(id)].filter((job) => job !== null) : store.listJobs();
      process.stdout.write(`${JSON.stringify(jobs, null, 2)}\n`);
      return;
    }
    if (command === "jobs" && action === "reconcile") {
      const id = arg("--id");
      const decision = arg("--decision");
      if (!id || (decision !== "sent" && decision !== "failed" && decision !== "delivery_unknown")) throw new Error("reconcile requires --id and --decision");
      const job = store.getJob(id);
      if (!job) throw new Error("job not found");
      const messageId = arg("--message-id") ?? null;
      if (decision === "sent" && !messageId) throw new Error("sent reconciliation requires --message-id");
      if (job.state !== "delivery_unknown" && job.state !== "failed") throw new Error("only failed or uncertain jobs can be reconciled");
      store.transition(id, job.state, decision as JobState, {
        teamsMessageId: messageId,
        errorCode: decision === "sent" ? null : decision,
        operatorReview: decision !== "sent",
      }, { action: "job.reconcile", detail: { decision, messageId } });
      process.stdout.write("reconciled\n");
      return;
    }
    if (command === "jobs" && action === "retry") {
      const id = arg("--id");
      const job = id ? store.getJob(id) : null;
      if (!id || !job) throw new Error("job not found");
      const uncertain = process.argv.includes("--confirm-uncertain");
      if ((job.operatorReview || job.state === "delivery_unknown" || store.hasDeliveryAttempt(id)) && !uncertain) throw new Error("operator review or previous delivery attempt requires --confirm-uncertain");
      if (job.state !== "failed" && job.state !== "delivery_unknown" && job.state !== "queued") throw new Error("job is not retryable");
      if (job.webhookDispatched && !job.draftJson && !job.generatedText) throw new Error("dispatched webhook cannot be replayed automatically");
      const next = job.draftJson || job.generatedText ? "ready_to_send" : "queued";
      store.transition(id, job.state, next, { errorCode: null, operatorReview: false }, { action: "job.retry", detail: { from: job.state, to: next, uncertain } });
      process.stdout.write("retry queued\n");
      return;
    }
    throw new Error("usage: credentials issue|revoke | jobs inspect|reconcile|retry");
  } finally {
    store.close();
  }
}

main();
