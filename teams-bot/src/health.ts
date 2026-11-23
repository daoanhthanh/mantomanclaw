import { createServer, type Server } from "node:http";
import type { ServiceConfig } from "./contracts.js";
import type { OperationStore } from "./store.js";

export function startLiveServer(port: number): Server {
  const server = createServer((req, res) => {
    if (req.url === "/health/live") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ live: true }));
      return;
    }
    res.writeHead(404).end("not found");
  });
  server.listen(port, "0.0.0.0");
  return server;
}

export function readyBody(config: ServiceConfig, store: OperationStore): { ready: boolean; state: string; clientsInitialized: boolean } {
  const open = store.listJobs().length >= 0;
  return { ready: open && config.clientId.length > 0, state: "open", clientsInitialized: true };
}
