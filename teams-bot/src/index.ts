import { App, ExpressAdapter } from "@microsoft/teams.apps";
import type { Server } from "node:http";
import { loadConfig } from "./config.js";
import type { ServiceConfig } from "./contracts.js";
import { createGraphClient, type GraphClient } from "./graph.js";
import { canonicalJson, newId, sha256Hex } from "./ids.js";
import { logEvent } from "./redact.js";
import { startMcpServer } from "./mcp.js";
import { OperationStore } from "./store.js";
import { classifyActivity, normalizeActivity } from "./teams.js";
import { extractDocument } from "./documents.js";
import { jobForTeams, processAvailable, recoverFailureNotices, type FileReader, type TeamsSender } from "./worker.js";

export type Runtime = {
  config: ServiceConfig;
  store: OperationStore;
  publicServer: { stop: () => Promise<void> };
  privateServer: Server;
  stop: () => Promise<void>;
};

export async function startRuntime(config: ServiceConfig, options: { graph?: GraphClient; sender?: TeamsSender; fetchImpl?: typeof fetch } = {}): Promise<Runtime> {
  const store = new OperationStore(config.statePath);
  store.recoverInterrupted();
  recoverFailureNotices(store);
  const graph = options.graph ?? createGraphClient(config);
  const adapter = new ExpressAdapter();
  adapter.get("/health/live", (_req: unknown, res: { status: (code: number) => { json: (body: unknown) => void } }) => {
    res.status(200).json({ live: true });
  });
  const app = new App({
    clientId: config.clientId,
    clientSecret: config.clientSecret,
    tenantId: config.microsoftTenantId,
    serviceUrl: config.teamsServiceUrl,
    httpServerAdapter: adapter,
  });
  const sender = options.sender ?? {
    async sendRoot(conversationId, activity) {
      const sent = await app.send(conversationId, activity);
      return { id: sent.id ?? "" };
    },
    async sendThread(channelOrChatId, rootMessageId, activity) {
      const sent = await app.reply(channelOrChatId, rootMessageId, activity);
      const id = sent && typeof sent === "object" && "id" in sent && typeof sent.id === "string" ? sent.id : "";
      return { id };
    },
  };
  app.on("message", async (ctx) => {
    const activity = normalizeActivity(ctx.activity);
    if (!activity) return;
    const decision = classifyActivity(config, activity);
    if (decision.kind === "ignore" || decision.kind === "reject") {
      logEvent("warn", "teams.activity_rejected", { code: decision.code });
      return;
    }
    store.saveRoute({
      tenantId: config.microsoftTenantId,
      teamId: decision.teamId,
      channelId: decision.channelId,
      conversationId: decision.conversationId,
      serviceUrl: decision.serviceUrl,
      rootReplyChainId: decision.rootThreadId || null,
      canPostRoot: decision.canPostRoot,
    });
    const requestHash = sha256Hex(canonicalJson({ text: decision.text, file: decision.kind === "clarify" ? undefined : decision.file, recipientId: decision.kind === "assignment" ? decision.recipientId : undefined }));
    const saved = store.insertJob(jobForTeams(config, {
      id: newId(),
      activityId: activity.id,
      actorId: decision.actorId,
      projectId: decision.projectId,
      conversationId: decision.conversationId,
      channelId: decision.channelId,
      rootThreadId: decision.rootThreadId,
      text: decision.kind === "clarify" ? decision.text : decision.text,
      kind: decision.kind === "clarify" ? "clarification" : decision.kind,
      requestHash,
      ...(decision.kind !== "clarify" && decision.file ? { file: decision.file } : {}),
      ...(decision.kind === "assignment" && decision.recipientId ? { recipientId: decision.recipientId } : {}),
    }));
    if (saved.status === "conflict") logEvent("warn", "teams.activity_conflict", { activityId: activity.id });
  });
  const reader: FileReader = {
    async read(projectId, payload) {
      const project = config.projects.find((item) => item.id === projectId);
      if (!project || !payload.file) throw new Error("file missing");
      const downloaded = await graph.readFile(project, payload.file);
      const extracted = await extractDocument(downloaded.name, downloaded.bytes);
      const coverage = [extracted.coverage, ...extracted.warnings].join("; ");
      return { name: downloaded.name, text: extracted.text, coverage, webUrl: downloaded.webUrl };
    },
  };
  let stopped = false;
  let inFlight: Promise<void> | undefined;
  const timer = setInterval(() => {
    if (inFlight || stopped) return;
    inFlight = processAvailable(config, store, sender, reader, options.fetchImpl)
      .then(() => undefined)
      .catch((error: unknown) => {
        logEvent("error", "worker.tick_failed", { message: error instanceof Error ? error.message : "error" });
      })
      .finally(() => { inFlight = undefined; });
  }, 500);
  timer.unref();
  await app.start(config.publicPort);
  const privateServer = startMcpServer(config, store, graph);
  await new Promise<void>((resolve) => privateServer.listen(config.privatePort, "0.0.0.0", resolve));
  return {
    config,
    store,
    publicServer: adapter,
    privateServer,
    async stop() {
      if (stopped) return;
      stopped = true;
      clearInterval(timer);
      await inFlight;
      await new Promise<void>((resolve, reject) => privateServer.close((error) => error ? reject(error) : resolve()));
      await app.stop();
      store.close();
    },
  };
}

async function main(): Promise<void> {
  const config = loadConfig();
  const runtime = await startRuntime(config);
  const shutdown = () => {
    void runtime.stop().finally(() => process.exit(0));
  };
  process.on("SIGTERM", shutdown);
  process.on("SIGINT", shutdown);
  logEvent("info", "teams_bot.started", { publicPort: config.publicPort, privatePort: config.privatePort });
}

if (process.argv[1]?.endsWith("index.ts") || process.argv[1]?.endsWith("index.js")) {
  main().catch((error: unknown) => {
    logEvent("error", "teams_bot.start_failed", { message: error instanceof Error ? error.message : "start failed" });
    process.exit(1);
  });
}
