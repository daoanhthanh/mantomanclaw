import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import test from "node:test";
import { downloadContent, GraphError } from "../src/graph.js";
import { authorizeFile } from "../src/policy.js";
import { testConfig } from "./fixture.js";

test("strips graph authorization on cross-host redirects and rejects private targets", async () => {
  const seen: string[] = [];
  const blob = createServer((req, res) => {
    seen.push(req.headers.authorization ?? "");
    res.end("file-bytes");
  });
  await listen(blob);
  const blobAddress = blob.address();
  if (!blobAddress || typeof blobAddress === "string") throw new Error("no blob port");
  const graph = createServer((req, res) => {
    seen.push(req.headers.authorization ?? "");
    res.writeHead(302, { location: `http://127.0.0.1:${blobAddress.port}/blob` });
    res.end();
  });
  await listen(graph);
  const graphAddress = graph.address();
  if (!graphAddress || typeof graphAddress === "string") throw new Error("no graph port");
  const bytes = await downloadContent(`http://127.0.0.1:${graphAddress.port}/content`, "secret-token", {
    allowInsecure: true,
    graphHost: "127.0.0.1",
    isPublicHost: () => true,
  });
  assert.equal(bytes.toString(), "file-bytes");
  assert.equal(seen[0], "Bearer secret-token");
  assert.equal(seen[1], "");
  await assert.rejects(downloadContent("https://127.0.0.1/file", "token"), (error: unknown) => error instanceof GraphError && error.code === "unsafe_download");
  blob.close();
  graph.close();
});

test("actor permitted in one project cannot read the other drive", () => {
  const config = testConfig();
  const phoenix = config.projects[0];
  const orion = config.projects[1];
  if (!phoenix || !orion) throw new Error("fixture missing");
  assert.equal(authorizeFile(phoenix, { kind: "drive-item", driveId: orion.driveId, itemId: "item" }).ok, false);
  assert.equal(authorizeFile(orion, { kind: "drive-item", driveId: phoenix.driveId, itemId: "item" }).ok, false);
});

function listen(server: Server): Promise<void> {
  return new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
}
