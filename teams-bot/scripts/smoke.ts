const base = process.env.TEAMS_BOT_SMOKE_URL ?? "http://127.0.0.1:3978";
const response = await fetch(new URL("/health/live", base), { signal: AbortSignal.timeout(5000) });
if (!response.ok) throw new Error(`liveness returned ${response.status}`);
const body: unknown = await response.json();
if (!body || typeof body !== "object" || !("live" in body) || body.live !== true) throw new Error("invalid liveness response");
console.log("Liveness passed; this does not verify Microsoft, Graph, gateway, or delivery acceptance.");
