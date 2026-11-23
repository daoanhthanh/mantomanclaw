import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";
import { App, ExpressAdapter } from "@microsoft/teams.apps";
import { classifyActivity, buildAssignmentActivity, messageLink } from "../src/teams.js";
import { activity, testConfig } from "./fixture.js";

test("rejects foreign tenant missing identity non-user and non-mention", () => {
  const config = testConfig();
  assert.equal(classifyActivity(config, activity({ channelData: { tenant: { id: "other" }, team: { id: "team-phoenix" }, channel: { id: "channel-phoenix" } }, conversation: { id: "c", tenantId: "other" } })).kind, "reject");
  const missing = classifyActivity(config, activity({ from: { id: "29:ada", role: "user" } }));
  assert.equal(missing.kind, "reject");
  if (missing.kind === "reject") assert.equal(missing.code, "missing_identity");
  const bot = classifyActivity(config, activity({ from: { id: "28:bot", aadObjectId: "entra-a", role: "bot" } }));
  if (bot.kind === "reject") assert.equal(bot.code, "non_user");
  const silent = classifyActivity(config, activity({ entities: [] }));
  if (silent.kind === "reject") assert.equal(silent.code, "not_mentioned");
});

test("formats root and thread mentions without dropping criteria", () => {
  const activityMessage = buildAssignmentActivity({
    title: "Implement export",
    summary: "Ship the export.",
    acceptanceCriteria: ["CSV download works"],
    sourceReferences: ["xlsx:req.xlsx:Scope!B12"],
  }, { teamsUserId: "29:alice", entraObjectId: "entra-alice", displayName: "Alice" }, "https://contoso.sharepoint.com/file");
  const payload = activityMessage.toInterface();
  assert.match(String(payload.text), /CSV download works/);
  const mention = payload.entities?.find((entity) => entity.type === "mention");
  assert.equal(mention?.mentioned.id, "29:alice");
  const root = classifyActivity(testConfig(), activity());
  assert.equal(root.kind, "assignment");
  if (root.kind === "assignment") assert.equal(root.canPostRoot, true);
  const thread = classifyActivity(testConfig(), activity({ replyToId: "root-1", channelData: { tenant: { id: "tenant-ms" }, team: { id: "team-phoenix" }, channel: { id: "channel-phoenix" }, replyChain: { id: "root-1" } } }));
  if (thread.kind === "assignment") assert.equal(thread.rootThreadId, "root-1");
  assert.match(messageLink("tenant-ms", "channel-phoenix", "message-1", "root-1"), /parentMessageId=root-1/);
});

test("sdk rejects a forged activity before a job exists", async () => {
  const server = createServer();
  const adapter = new ExpressAdapter(server);
  const app = new App({
    clientId: "client-1",
    clientSecret: "client-secret-value",
    tenantId: "tenant-ms",
    httpServerAdapter: adapter,
  });
  await app.start(0);
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("no port");
  const response = await fetch(`http://127.0.0.1:${address.port}/api/messages`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: "Bearer forged" },
    body: JSON.stringify({ type: "message", id: "forged", text: "assign" }),
  });
  assert.notEqual(response.status, 200);
  await app.stop();
});
