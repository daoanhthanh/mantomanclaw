import { ConfidentialClientApplication } from "@azure/msal-node";
import type { FileReference, ProjectConfig, ServiceConfig } from "./contracts.js";
import { authorizeFile } from "./policy.js";
import { logEvent } from "./redact.js";

const GRAPH = "https://graph.microsoft.com/v1.0";
const MAX_REDIRECTS = 3;
const MAX_BYTES = 8 * 1024 * 1024;

export class GraphError extends Error {
  constructor(readonly code: string, message: string) {
    super(message);
    this.name = "GraphError";
  }
}

export type GraphClient = {
  readFile(project: ProjectConfig, file: FileReference): Promise<{ name: string; bytes: Buffer; webUrl: string; eTag: string | null }>;
};

type DownloadDeps = {
  fetchImpl?: typeof fetch;
  isPublicHost?: (hostname: string) => boolean;
  graphHost?: string;
  allowInsecure?: boolean;
  timeoutMs?: number;
};

export function createGraphClient(config: ServiceConfig, deps: DownloadDeps = {}): GraphClient {
  const app = new ConfidentialClientApplication({
    auth: {
      clientId: config.clientId,
      clientSecret: config.clientSecret,
      authority: `https://login.microsoftonline.com/${config.microsoftTenantId}`,
    },
  });
  return {
    async readFile(project, file) {
      const token = await tokenFor(app);
      const item = await resolveItem(token, project, file, deps);
      const bytes = await downloadContent(item.contentUrl, token, deps);
      return { name: item.name, bytes, webUrl: item.webUrl, eTag: item.eTag };
    },
  };
}

async function tokenFor(app: ConfidentialClientApplication): Promise<string> {
  const result = await app.acquireTokenByClientCredential({ scopes: ["https://graph.microsoft.com/.default"] });
  if (!result?.accessToken) throw new GraphError("graph_denied", "client credential token was not issued");
  return result.accessToken;
}

type ItemMeta = { name: string; webUrl: string; eTag: string | null; contentUrl: string; siteId?: string; driveId?: string; path?: string; host?: string };

async function resolveItem(token: string, project: ProjectConfig, file: FileReference, deps: DownloadDeps): Promise<ItemMeta> {
  const scoped = authorizeFile(project, file);
  if (!scoped.ok) throw new GraphError(scoped.code, scoped.detail);
  const fetchImpl = deps.fetchImpl ?? fetch;
  let url: string;
  if (file.kind === "drive-item") {
    if (!file.driveId || !file.itemId) throw new GraphError("file_outside_scope", "drive item reference is incomplete");
    url = `${GRAPH}/drives/${encodeURIComponent(file.driveId)}/items/${encodeURIComponent(file.itemId)}?$select=id,name,size,parentReference,file,eTag,webUrl`;
  } else {
    if (!file.url) throw new GraphError("file_outside_scope", "sharepoint URL is missing");
    url = `${GRAPH}/shares/${encodeURIComponent(shareId(file.url))}/driveItem?$select=id,name,size,parentReference,file,eTag,webUrl`;
  }
  const response = await fetchImpl(url, {
    headers: { authorization: `Bearer ${token}`, accept: "application/json" },
    redirect: "error",
    signal: AbortSignal.timeout(deps.timeoutMs ?? 30_000),
  });
  if (response.status === 401 || response.status === 403) throw new GraphError("graph_denied", "graph denied the selected resource");
  if (!response.ok) throw new GraphError("graph_denied", `graph metadata failed with ${response.status}`);
  const body = await response.json() as { id?: string; name?: string; webUrl?: string; eTag?: string; parentReference?: { siteId?: string; driveId?: string; path?: string } };
  const webUrl = body.webUrl ?? file.url ?? "";
  let host: string | undefined;
  try {
    host = webUrl ? new URL(webUrl).hostname : undefined;
  } catch {
    host = undefined;
  }
  const resolved = {
    siteId: body.parentReference?.siteId,
    driveId: body.parentReference?.driveId,
    path: body.parentReference?.path,
    host,
  };
  const checked = authorizeFile(project, file, resolved);
  if (!checked.ok) throw new GraphError(checked.code, checked.detail);
  if (!body.id || !body.parentReference?.driveId) throw new GraphError("graph_denied", "graph item identity was incomplete");
  return {
    name: body.name ?? "document",
    webUrl,
    eTag: body.eTag ?? null,
    contentUrl: `${GRAPH}/drives/${encodeURIComponent(body.parentReference.driveId)}/items/${encodeURIComponent(body.id)}/content`,
    ...resolved,
  };
}

export async function downloadContent(url: string, token: string, deps: DownloadDeps = {}): Promise<Buffer> {
  const fetchImpl = deps.fetchImpl ?? fetch;
  const isPublicHost = deps.isPublicHost ?? defaultPublicHost;
  let current = url;
  let bearer: string | null = token;
  const chunks: Buffer[] = [];
  let size = 0;
  const signal = AbortSignal.timeout(deps.timeoutMs ?? 30_000);
  for (let hop = 0; hop <= MAX_REDIRECTS; hop += 1) {
    const parsed = new URL(current);
    if (parsed.protocol !== "https:" && !deps.allowInsecure) throw new GraphError("unsafe_download", "download target must be https");
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") throw new GraphError("unsafe_download", "download target scheme is rejected");
    if (!isPublicHost(parsed.hostname)) throw new GraphError("unsafe_download", "download target is not a public host");
    const headers: Record<string, string> = {};
    if (bearer && parsed.hostname === (deps.graphHost ?? "graph.microsoft.com")) headers.authorization = `Bearer ${bearer}`;
    const response = await fetchImpl(current, { headers, redirect: "manual", signal });
    if (response.status >= 300 && response.status < 400) {
      const location = response.headers.get("location");
      if (!location) throw new GraphError("unsafe_download", "redirect was missing a location");
      current = new URL(location, current).toString();
      bearer = null;
      logEvent("info", "graph.redirect", { host: new URL(current).hostname });
      continue;
    }
    if (response.status === 401 || response.status === 403) throw new GraphError("graph_denied", "graph content access was denied");
    if (!response.ok) throw new GraphError("graph_denied", `content download failed with ${response.status}`);
    if (!response.body) throw new GraphError("graph_denied", "content body was empty");
    const reader = response.body.getReader();
    for (;;) {
      const step = await reader.read();
      if (step.done) break;
      size += step.value.byteLength;
      if (size > MAX_BYTES) throw new GraphError("document_too_large", "download exceeded the byte cap");
      chunks.push(Buffer.from(step.value));
    }
    return Buffer.concat(chunks);
  }
  throw new GraphError("unsafe_download", "redirect limit exceeded");
}

export function shareId(url: string): string {
  return `u!${Buffer.from(url, "utf8").toString("base64url")}`;
}

function defaultPublicHost(hostname: string): boolean {
  if (hostname === "localhost" || hostname.endsWith(".local")) return false;
  if (isPrivateIp(hostname)) return false;
  return hostname === "graph.microsoft.com" || hostname.endsWith(".sharepoint.com") || hostname.endsWith(".sharepoint-df.com");
}

function isPrivateIp(hostname: string): boolean {
  const ipv4 = hostname.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/);
  if (!ipv4) return hostname === "::1" || hostname.startsWith("fe80:") || hostname.startsWith("fc") || hostname.startsWith("fd");
  const parts = ipv4.slice(1).map(Number);
  const [a, b] = parts;
  if (a === undefined || b === undefined) return true;
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254) || a === 0;
}
