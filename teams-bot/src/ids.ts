import { createHash, randomBytes, randomUUID, timingSafeEqual } from "node:crypto";

export function sha256Hex(value: string | Buffer): string {
  return createHash("sha256").update(value).digest("hex");
}

export function newId(): string {
  return randomUUID();
}

export function issueToken(): string {
  return `tbc_${randomBytes(32).toString("base64url")}`;
}

export function tokenHash(token: string): string {
  return sha256Hex(token);
}

export function safeEqual(left: string, right: string): boolean {
  const a = Buffer.from(left);
  const b = Buffer.from(right);
  if (a.length !== b.length) return false;
  return timingSafeEqual(a, b);
}

export function canonicalJson(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map((item) => sortValue(item));
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(value as Record<string, unknown>).sort()) {
      out[key] = sortValue((value as Record<string, unknown>)[key]);
    }
    return out;
  }
  return value;
}

export function versionedKey(prefix: string, parts: string[]): string {
  for (const part of parts) {
    if (typeof part !== "string" || part.trim() === "") {
      throw new Error("empty_identity");
    }
  }
  return `${prefix}${sha256Hex(JSON.stringify(parts))}`;
}

export function sessionKey(parts: {
  installationId: string;
  agentId: string;
  projectId: string;
  actorId: string;
  conversationId: string;
  rootThreadId: string;
}): string {
  const required = [parts.installationId, parts.agentId, parts.projectId, parts.actorId, parts.conversationId];
  if (required.some((part) => typeof part !== "string" || part.trim() === "") || typeof parts.rootThreadId !== "string") {
    throw new Error("empty_identity");
  }
  return `teams:v1:${sha256Hex(JSON.stringify([...required, parts.rootThreadId]))}`;
}

export function syntheticUserId(parts: { installationId: string; projectId: string; actorId: string }): string {
  return versionedKey("teams-user:v1:", [parts.installationId, parts.projectId, parts.actorId]);
}
