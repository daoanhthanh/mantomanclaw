const SECRET_KEY = /secret|token|authorization|signature|password|webhook|credential/i;

export function redact(value: unknown): unknown {
  if (typeof value === "string") return redactText(value);
  if (Array.isArray(value)) return value.map((item) => redact(item));
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = SECRET_KEY.test(key) ? "[redacted]" : redact(item);
    }
    return out;
  }
  return value;
}

export function redactText(value: string): string {
  return value
    .replace(/Bearer\s+[A-Za-z0-9._~+/-]+=*/gi, "Bearer [redacted]")
    .replace(/sig=[^&\s]+/gi, "sig=[redacted]")
    .replace(/tempauth=[^&\s]+/gi, "tempauth=[redacted]")
    .replace(/https:\/\/[^/\s]+\/[^?\s]*\?[^)\s]+/gi, "[redacted-url]");
}

export function logEvent(level: "info" | "warn" | "error", event: string, fields: Record<string, unknown> = {}): void {
  const line = JSON.stringify({ level, event, ...redact(fields) as Record<string, unknown> });
  if (level === "error") console.error(line);
  else if (level === "warn") console.warn(line);
  else console.log(line);
}
