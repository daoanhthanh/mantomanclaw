import assert from "node:assert/strict";
import test from "node:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { startMcpServer } from "../src/mcp.js";
import { OperationStore } from "../src/store.js";
import { testConfig } from "./fixture.js";

test("mcp auth is bearer-bound and operation keys conflict on changed bodies", async () => {
  const config = testConfig({ privatePort: 0 });
  const store = new OperationStore(":memory:");
  const issued = store.issueCredential("actor-a");
  const other = store.issueCredential("actor-b");
  store.saveRoute({ tenantId: config.microsoftTenantId, teamId: "team-phoenix", channelId: "channel-phoenix", conversationId: "conversation-1", serviceUrl: config.teamsServiceUrl, rootReplyChainId: null, canPostRoot: true });
  const server = startMcpServer(config, store, null);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("no port");
  const url = new URL(`http://127.0.0.1:${address.port}/mcp`);
  const unauthenticated = await fetch(url, { method: "POST", headers: { "content-type": "application/json", host: "127.0.0.1" }, body: "{}" });
  assert.equal(unauthenticated.status, 401);
  const client = await connect(url, issued.token);
  const resources = await client.callTool({ name: "project_resources", arguments: { project: "phoenix", actorId: "actor-b" } });
  assert.equal(resources.isError, true);
  const posted = await client.callTool({ name: "post_assignment", arguments: draftArgs("op-same-1") });
  assert.notEqual(posted.isError, true, textOf(posted));
  const first = JSON.parse(textOf(posted)) as { operation_id: string; status: string; error_code: string | null };
  assert.equal(first.status, "queued");
  assert.equal(first.error_code, null);
  const repeat = JSON.parse(textOf(await client.callTool({ name: "post_assignment", arguments: draftArgs("op-same-1") }))) as { operation_id: string };
  assert.equal(repeat.operation_id, first.operation_id);
  const changed = JSON.parse(textOf(await client.callTool({ name: "post_assignment", arguments: { ...draftArgs("op-same-1"), draft: { ...draftArgs("op-same-1").draft, title: "Changed" } } }))) as { error_code: string };
  assert.equal(changed.error_code, "idempotency_conflict");
  const foreign = await connect(url, other.token);
  const hidden = JSON.parse(textOf(await foreign.callTool({ name: "assignment_status", arguments: { operation_id: first.operation_id } }))) as { error_code: string };
  assert.equal(hidden.error_code, "unknown_operation");
  const cross = JSON.parse(textOf(await client.callTool({ name: "project_resources", arguments: { project: "orion" } }))) as { error_code: string };
  assert.equal(cross.error_code, "actor_not_permitted");
  await client.close();
  await foreign.close();
  server.close();
  store.close();
});

function draftArgs(operationKey: string) {
  return {
    project: "phoenix",
    operation_key: operationKey,
    recipient: "alice",
    draft: { title: "Export", summary: "Ship it", acceptanceCriteria: ["works"], sourceReferences: ["docx:p1"] },
  };
}

async function connect(url: URL, token: string): Promise<Client> {
  const client = new Client({ name: "test", version: "0" });
  const transport = new StreamableHTTPClientTransport(url, { requestInit: { headers: { authorization: `Bearer ${token}` } } });
  await client.connect(transport);
  return client;
}

function textOf(result: unknown): string {
  if (!result || typeof result !== "object" || !("content" in result) || !Array.isArray(result.content)) {
    throw new Error("missing text content");
  }
  const textItem = result.content.find((item: unknown) => item && typeof item === "object" && "type" in item && item.type === "text" && "text" in item && typeof item.text === "string");
  if (!textItem || typeof textItem !== "object" || !("text" in textItem) || typeof textItem.text !== "string") throw new Error("missing text content");
  return textItem.text;
}
