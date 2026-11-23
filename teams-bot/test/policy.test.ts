import assert from "node:assert/strict";
import test from "node:test";
import { loadConfigFromObject } from "../src/config.js";
import { sessionKey, syntheticUserId } from "../src/ids.js";
import { authorizeActorProject, authorizeFile, resolveRecipient } from "../src/policy.js";
import { startLiveServer } from "../src/health.js";
import { testConfig } from "./fixture.js";

test("policy rejects unknown tenant actor project destination and cross-project access", () => {
  const config = testConfig();
  assert.equal(authorizeActorProject(config, "missing", "phoenix").ok, false);
  assert.equal(authorizeActorProject(config, "actor-a", "missing").ok, false);
  const cross = authorizeActorProject(config, "actor-a", "orion");
  assert.equal(cross.ok, false);
  if (!cross.ok) assert.equal(cross.code, "actor_not_permitted");
  const phoenix = authorizeActorProject(config, "actor-a", "phoenix");
  assert.equal(phoenix.ok, true);
  if (!phoenix.ok) return;
  const outside = authorizeFile(phoenix.value.project, { kind: "drive-item", driveId: "drive-orion", itemId: "item-1" });
  assert.equal(outside.ok, false);
  const foreignHost = authorizeFile(phoenix.value.project, { kind: "sharepoint-url", url: "https://evil.example/file.docx" });
  assert.equal(foreignHost.ok, false);
  const ambiguous = resolveRecipient(phoenix.value.project, { alias: "ali" });
  assert.equal(ambiguous.ok, false);
});

test("empty mapped identity is rejected", () => {
  assert.throws(() => syntheticUserId({ installationId: "install-1", projectId: "", actorId: "actor-a" }), /empty_identity/);
  assert.throws(() => sessionKey({
    installationId: "install-1",
    agentId: "agent",
    projectId: "phoenix",
    actorId: "actor-a",
    conversationId: "",
    rootThreadId: "",
  }), /empty_identity/);
});

test("actor and project session keys do not collide", () => {
  const keys = [
    sessionKey({ installationId: "i", agentId: "ap", projectId: "phoenix", actorId: "actor-a", conversationId: "c", rootThreadId: "" }),
    sessionKey({ installationId: "i", agentId: "ap", projectId: "phoenix", actorId: "actor-b", conversationId: "c", rootThreadId: "" }),
    sessionKey({ installationId: "i", agentId: "ao", projectId: "orion", actorId: "actor-a", conversationId: "c", rootThreadId: "" }),
  ];
  assert.equal(new Set(keys).size, 3);
  assert.notEqual(
    syntheticUserId({ installationId: "i", projectId: "phoenix", actorId: "actor-a" }),
    syntheticUserId({ installationId: "i", projectId: "orion", actorId: "actor-a" }),
  );
});

test("deadline must exceed gateway timeout and health live does not contact Microsoft", async () => {
  assert.throws(() => loadConfigFromObject(testConfig({ httpDeadlineSec: 600 })), /exceed/);
  const server = startLiveServer(0);
  await new Promise<void>((resolve) => server.once("listening", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("no port");
  const response = await fetch(`http://127.0.0.1:${address.port}/health/live`);
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { live: true });
  await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
});
