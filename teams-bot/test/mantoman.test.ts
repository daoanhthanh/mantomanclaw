import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";
import { buildInput, invokeWebhook, parseDraft, signWebhook } from "../src/mantoman.js";
import { testConfig } from "./fixture.js";

test("signs exact bytes and treats failure pending and truncated envelopes as non-deliverable", async () => {
  const body = JSON.stringify({ input: "hello", session_key: "teams:v1:abc", user_id: "teams-user:v1:abc", mode: "sync" });
  const first = signWebhook("webhook-secret-value", 100, Buffer.from(body));
  const second = signWebhook("webhook-secret-value", 101, Buffer.from(body));
  assert.notEqual(first, second);
  assert.match(first, /^t=100,v1=[0-9a-f]+$/);
  const server = createServer((req, res) => {
    const url = req.url ?? "";
    if (url.startsWith("/pending/")) {
      res.writeHead(202, { "content-type": "application/json" });
      res.end(JSON.stringify({ status: "running", call_id: "call-pending" }));
      return;
    }
    if (url.startsWith("/failure/")) {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ call_id: "call-fail", agent_id: "agent", output: "", finish_reason: "stop", error: "stored failure" }));
      return;
    }
    if (url.startsWith("/truncated/")) {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(`${"x".repeat(32 * 1024 + 1)}`);
      return;
    }
    const signature = req.headers["x-mantomanagent-signature"];
    assert.equal(typeof signature, "string");
    assert.equal(req.headers["idempotency-key"], "op-1");
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ call_id: "call-ok", agent_id: "agent-phoenix", output: JSON.stringify({ title: "T", summary: "S", acceptanceCriteria: ["A"], sourceReferences: ["docx:p1"] }), finish_reason: "stop" }));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("no port");
  const config = testConfig({ mantomanBaseUrl: `http://127.0.0.1:${address.port}`, httpDeadlineSec: 5 });
  const project = config.projects[0];
  if (!project) throw new Error("missing project fixture");
  const ok = await invokeWebhook(config, project, body, "op-1");
  assert.equal(ok.kind, "output");
  if (ok.kind === "output") assert.equal(parseDraft(ok.output).title, "T");
  const pending = await invokeWebhook({ ...config, mantomanBaseUrl: `${config.mantomanBaseUrl}/pending` }, project, body, "op-1");
  assert.equal(pending.kind, "pending");
  const failure = await invokeWebhook({ ...config, mantomanBaseUrl: `${config.mantomanBaseUrl}/failure` }, project, body, "op-1");
  assert.equal(failure.kind, "failure");
  const truncated = await invokeWebhook({ ...config, mantomanBaseUrl: `${config.mantomanBaseUrl}/truncated` }, project, body, "op-1");
  assert.equal(truncated.kind, "failure");
  assert.throws(() => buildInput({ instruction: "x", documents: [{ label: "required.docx", text: "y".repeat(130 * 1024), coverage: "all", required: true }] }), /required_document_omitted/);
  assert.throws(() => parseDraft(JSON.stringify({ title: 42, summary: "summary", acceptanceCriteria: ["works"], sourceReferences: ["source"] })), /invalid_draft/);
  assert.throws(() => buildInput({ instruction: "required.docx", documents: [{ label: "required.docx", text: "x".repeat(130 * 1024), coverage: "all", required: true }] }), /required_document_omitted/);
  server.close();
});

test("webhook requests use the selected project's credential", async () => {
  const config = testConfig();
  const [first, second] = config.projects;
  if (!first || !second) throw new Error("missing project fixtures");
  const body = "{\"input\":\"requirements\"}";
  const requests: Array<{ id: string | null; signature: string | null }> = [];
  const capture: typeof fetch = async (_url, init) => {
    const headers = new Headers(init?.headers);
    requests.push({ id: headers.get("x-webhook-id"), signature: headers.get("x-mantomanagent-signature") });
    return new Response(JSON.stringify({ call_id: "call", agent_id: "agent", output: "answer", finish_reason: "stop" }));
  };
  await invokeWebhook(config, first, body, "first-operation", capture, () => 100_000);
  await invokeWebhook(config, second, body, "second-operation", capture, () => 100_000);
  assert.deepEqual(requests, [
    { id: first.webhookId, signature: signWebhook(first.webhookSecret, 100, Buffer.from(body)) },
    { id: second.webhookId, signature: signWebhook(second.webhookSecret, 100, Buffer.from(body)) },
  ]);
});
