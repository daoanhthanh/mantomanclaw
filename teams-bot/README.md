# teams-bot

A standalone TypeScript service that connects Microsoft Teams to Mantoman agents.
It handles two independent flows: Teams mentions that trigger an agent run, and
Mantoman app-chat that posts assignments directly to a Teams channel via MCP tools.

> **Acceptance status:** local compilation, tests, and Docker build pass.
> Real-tenant acceptance (Teams mentions, Graph file access, live MCP posting) has
> not been run yet. See [Remaining before go-live](#remaining-before-go-live).

---

## Architecture

```
Teams channel
  │  @bot assign recipient:alice file:<SharePoint URL>
  ▼
┌─────────────────────────────────────────────────────┐
│  teams-bot  (port 3978 public, port 3979 private)   │
│                                                     │
│  Teams SDK  ──► Activity handler                    │
│                  │  validate tenant/actor/project   │
│                  │  persist job atomically          │
│                  │  ack immediately (no long-poll)  │
│                  ▼                                  │
│             Worker loop                             │
│               │  read DOCX/XLSX via Graph           │
│               │  HMAC-sign + POST /v1/webhooks/llm  │
│               │  parse validated JSON draft         │
│               │  send Teams reply with @mention     │
│               │  persist Teams message ID           │
│                                                     │
│  MCP server (port 3979) ◄─── Mantoman app-chat      │
│    project_resources                                │
│    read_requirement      per-user bearer auth       │
│    post_assignment  ─────────────────────────────►  │
│    assignment_status       Teams channel            │
│                                                     │
│  SQLite  (named volume /app/data)                   │
│    jobs · routes · credentials · audit              │
└─────────────────────────────────────────────────────┘
         │                        │
   Microsoft Graph          Mantoman gateway
   (SharePoint files)       POST /v1/webhooks/llm
```

### Flow 1 — Teams → Mantoman → Teams

1. A configured actor mentions `@bot` in an approved channel.
2. The Teams SDK validates the inbound JWT (Microsoft-issued). The activity handler
   checks tenant, actor, project, and destination, then persists a `queued` job and
   returns HTTP 200 immediately.
3. The worker picks up the job: reads the linked DOCX or XLSX via Microsoft Graph
   (application `Sites.Selected` permission), builds bounded UTF-8 source text
   (≤ 128 KiB), and POSTs to the Mantoman LLM webhook with an HMAC signature.
4. The agent response is validated. For assignment requests the output must be a JSON
   draft `{ title, summary, acceptanceCriteria, sourceReferences }`. For plain
   questions it is bounded plain text.
5. The service sends one Teams reply in the source thread with real `@mention` for the
   recipient, persists the Teams message ID, and transitions the job to `sent`.

Session key is `"teams:v1:" + sha256(JSON.stringify([installationId, agentId, projectId, actorId, conversationId, rootThreadId]))`.
This prevents cross-project or cross-actor history sharing regardless of timing.

### Flow 2 — Mantoman app-chat → MCP → Teams

1. A user pastes a SharePoint link and recipient alias into existing app chat.
2. The designated app agent calls `post_assignment` on the MCP server
   (`http://teams-bot:3979/mcp`, Streamable HTTP).
3. The service authenticates the bearer token, resolves actor/project/recipient from
   the token hash (never from tool arguments), atomically reserves an operation ID,
   and enqueues the job.
4. The agent-generated draft travels through the same delivery queue as flow 1;
   no second LLM call is made.
5. `assignment_status` returns `{ operation_id, status, message_id, message_url }`.

### Job state machine

```
queued ──► reading ──► generating ──► ready_to_send ──► sending ──► sent
                                                                 └──► delivery_unknown
any stage ──────────────────────────────────────────────────────────► failed
```

Restart safety: `reading` and `generating` jobs are marked for operator review (not
auto-retried). `sending` jobs become `delivery_unknown`. Only `queued` jobs are
requeued on startup.

---

## Project layout

```
teams-bot/
├── src/
│   ├── index.ts        # Runtime entry: Teams SDK adapter + public listener (3978)
│   ├── config.ts       # Config loader + validation (env + JSON file)
│   ├── contracts.ts    # Shared types: ServiceConfig, JobRecord, JobState, …
│   ├── policy.ts       # Default-deny actor/project/file/destination checks
│   ├── store.ts        # SQLite OperationStore (jobs, routes, credentials, audit)
│   ├── worker.ts       # Job processor: read → webhook → send state machine
│   ├── mantoman.ts     # Webhook call: HMAC signing, session key, draft parsing
│   ├── teams.ts        # Activity normalisation, classification, Teams message builder
│   ├── graph.ts        # Microsoft Graph client (drive items, file download)
│   ├── documents.ts    # DOCX/XLSX text extraction + coverage metadata
│   ├── mcp.ts          # Streamable HTTP MCP server (port 3979) + four tools
│   ├── ids.ts          # sha256, session key, synthetic user ID, token issuance
│   ├── redact.ts       # Structured logger that strips secrets from log output
│   ├── health.ts       # /health/live and /health/ready handlers
│   ├── archive.ts      # Pagination helper for large Graph result sets
│   └── cli.ts          # Operator CLI: credentials issue/revoke, jobs inspect/reconcile
├── scripts/
│   ├── package-app.ts  # Generates teams-app.zip from appPackage/ + env vars
│   └── smoke.ts        # Live smoke test against a real gateway
├── test/               # Node test runner; 20 tests across all modules
├── appPackage/
│   ├── manifest.template.json
│   ├── color.png
│   └── outline.png
├── config.example.json # Copy to config.json; replace all placeholder values
├── .env.example        # Copy to .env; add real secrets; never commit
├── Dockerfile          # Node 24 multi-stage, non-root, frozen lockfile
└── .dockerignore
```

---

## Prerequisites

Before running anything you need:

| Item | Where to get it |
|---|---|
| Azure Bot registration | [Azure Portal → Bot Services](https://portal.azure.com) |
| Entra app (client ID + secret) | Azure Portal → Entra ID → App registrations |
| Microsoft Teams channel enabled | Plan phase 2, or run `az rest` from the provisioning runbook |
| SharePoint site with `Sites.Selected` grant | Microsoft 365 admin + Graph app consent |
| Mantoman gateway running | `./mantomanagent` or the standard Docker stack |
| Mantoman LLM webhook | Gateway web UI → Webhooks → new webhook with `require_hmac=true` |
| HTTPS messaging endpoint | Cloudflare Tunnel, ngrok, or a reverse proxy → `https://<host>/api/messages` |

For local dev without real Microsoft connectivity, the test suite uses HTTP fixtures
and an in-memory SQLite database — no Azure account required.

---

## Configuration

### 1. Copy the example files

```sh
cp teams-bot/config.example.json teams-bot/config.json
cp teams-bot/.env.example         teams-bot/.env
```

### 2. Edit `config.json`

```jsonc
{
  // Unique ID for this installation (any stable UUID you choose)
  "installationId": "replace-installation-id",

  // Azure AD / Entra tenant ID
  "microsoftTenantId": "replace-microsoft-tenant-id",

  // Mantoman tenant ID (from gateway config)
  "mantomanTenantId": "replace-mantoman-tenant-id",

  // Gateway base URL — use Docker service name when running in Compose
  "mantomanBaseUrl": "http://mantomanagent:18790",

  // Entra app (bot) client ID
  "clientId": "replace-client-id",

  // Teams service URL for your region (default is correct for most tenants)
  "teamsServiceUrl": "https://smba.trafficmanager.net/teams",

  // SQLite file path (overridden to /app/data/teams-bot.sqlite in Docker)
  "statePath": "./data/teams-bot.sqlite",

  // Match the deployed gateway sync timeout; httpDeadlineSec must exceed it
  "gatewaySyncTimeoutSec": 600,
  "httpDeadlineSec": 630,

  // Actors — people who may invoke the service
  "actors": [{
    "id": "alice",                                 // internal alias
    "mantomanUserId": "replace-mantoman-user-id",  // from gateway user list
    "entraObjectId": "replace-entra-object-id",    // from Azure Entra
    "displayName": "Alice Example"
  }],

  // Projects — one per Teams channel / SharePoint library pair
  "projects": [{
    "id": "example",
    "aliases": ["example"],
    "webhookId": "replace-project-webhook-id",     // from gateway Webhooks page
    "webhookSecretEnv": "TEAMS_WEBHOOK_EXAMPLE_SECRET", // env var name (not value)
    "agentId": "replace-project-agent-id",         // dedicated webhook agent
    "siteId": "replace-site-id",                   // SharePoint site ID
    "driveId": "replace-drive-id",                 // document library drive ID
    "libraryPathPrefix": "Requirements",           // restrict to this folder
    "allowedHosts": ["yourcompany.sharepoint.com"],
    "actors": ["alice"],
    "destination": {
      "teamId": "replace-team-id",
      "channelId": "replace-channel-id"
    },
    "recipients": [{
      "alias": "alice",
      "entraObjectId": "replace-entra-object-id",
      "teamsUserId": "replace-teams-user-id",
      "displayName": "Alice Example"
    }]
  }]
}
```

### 3. Edit `teams-bot/.env`

```sh
TEAMS_BOT_CONFIG=./config.json
MICROSOFT_CLIENT_SECRET=<your-client-secret>
TEAMS_WEBHOOK_EXAMPLE_SECRET=<raw-webhook-secret-from-gateway>
```

Secret names in `.env` must match the `webhookSecretEnv` values in `config.json`.
Never commit either file.

---

## Running locally (dev)

```sh
# Install dependencies
pnpm --dir teams-bot install --frozen-lockfile

# Type-check
pnpm --dir teams-bot typecheck

# Run tests (no real credentials needed)
pnpm --dir teams-bot test

# Start dev server (hot-reload via tsx)
# Requires config.json and .env to be filled in with real values
pnpm --dir teams-bot dev
```

The public listener starts on `http://localhost:3978`.
The MCP server starts on `http://localhost:3979`.

For Teams to reach the bot you need an HTTPS endpoint. The simplest option:

```sh
# Using cloudflared (install via brew install cloudflared)
cloudflared tunnel --url http://localhost:3978
# Copy the https://…trycloudflare.com URL
# Set it as the messaging endpoint in your Azure Bot resource configuration
```

---

## Running in Docker (combined stack)

The `docker-compose.teams.yml` file is an **additive overlay**; it cannot run alone.

```sh
# 1. Set required env vars (or add them to .env.local at the repo root)
export MANTOMANAGENT_GATEWAY_TOKEN=<gateway-token>
export MICROSOFT_CLIENT_SECRET=<client-secret>

# 2. Bring up the full stack
docker compose \
  -f docker-compose.yml \
  -f docker-compose.postgres.yml \
  -f docker-compose.teams.yml \
  up -d --build

# 3. Check the service is live
curl http://localhost:3978/health/live
```

What the overlay adds:

- `teams-bot` service: Node 24, non-root `node` user, dropped Linux capabilities,
  `no-new-privileges`, read-only filesystem with `/tmp` tmpfs.
- Port `127.0.0.1:3978` published (public bot listener). Port `3979` is **not**
  published — MCP stays on internal Docker networks only.
- Named volume `teams-bot-data` at `/app/data` for the SQLite database.
- Joins both `default` and `mantomanagent-net` networks so it can reach the gateway
  at `http://mantomanagent:18790`.
- `depends_on: mantomanagent: condition: service_started`. This is startup ordering
  only; the worker queues jobs until a live gateway connection is available.

---

## Teams app package

```sh
# Build the .zip to upload to Teams Admin Center
export TEAMS_APP_ID=<stable-uuid-for-this-app>
export TEAMS_BOT_ID=<your-entra-app-client-id>
export TEAMS_APP_HOST=<your-public-hostname>   # e.g. teams.example.com

pnpm --dir teams-bot run package-app
# writes teams-bot/teams-app.zip
```

Upload `teams-app.zip` to Teams Admin Center → Manage apps → Upload.
Install it into your test team.
An authorized actor must then `@mention` the bot in each approved channel once
to register the conversation route.

**Assignment syntax:**

```
@YourBotName assign recipient:alice file:https://company.sharepoint.com/sites/…/file.docx
```

For plain questions simply mention the bot without `assign`.

---

## MCP tools (Mantoman app-chat)

Register the MCP server in your gateway config for the designated project agent:

| Setting | Value |
|---|---|
| URL | `http://teams-bot:3979/mcp` |
| Transport | `streamable-http` |
| `require_user_credentials` | `true` |
| Allowed tools | `project_resources`, `read_requirement`, `post_assignment`, `assignment_status` |

Add `teams-bot` to `MANTOMANAGENT_MCP_ALLOWED_HOSTS` — but keep all existing hosts.
This env var **replaces** the full list, not appends to it.

| Tool | What it does |
|---|---|
| `project_resources` | Returns allowed file scopes, recipient aliases, destination |
| `read_requirement` | Downloads a SharePoint file and returns bounded labelled text |
| `post_assignment` | Enqueues a Teams assignment, returns `operation_id` + status |
| `assignment_status` | Returns current state and delivery receipt for an operation |

---

## Operator CLI

Issue and revoke per-user MCP credentials, and inspect/reconcile jobs.

**Local dev:**

```sh
pnpm --dir teams-bot run cli credentials issue   --actor alice
pnpm --dir teams-bot run cli credentials revoke  --id CREDENTIAL_ID
pnpm --dir teams-bot run cli jobs inspect         --id OPERATION_ID
pnpm --dir teams-bot run cli jobs reconcile       --id OPERATION_ID \
  --decision sent --message-id TEAMS_MESSAGE_ID
```

**Combined Docker stack:**

```sh
COMPOSE="docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.teams.yml"

$COMPOSE exec teams-bot node dist/src/cli.js credentials issue   --actor alice
$COMPOSE exec teams-bot node dist/src/cli.js credentials revoke  --id CREDENTIAL_ID
$COMPOSE exec teams-bot node dist/src/cli.js jobs inspect         --id OPERATION_ID
$COMPOSE exec teams-bot node dist/src/cli.js jobs reconcile       --id OPERATION_ID \
  --decision sent --message-id TEAMS_MESSAGE_ID
```

Each credential token is printed **once** at issuance. Install it via the gateway's
per-user MCP settings for the actor. Revoke immediately if compromised.

Only reconcile a `delivery_unknown` job after independently verifying the Teams
message ID in the channel — do not guess.

---

## Rollback

1. Stop only `teams-bot`: `docker compose … stop teams-bot`
2. Revoke MCP credentials for all actors via the CLI.
3. Revoke the bot's webhook secret in the gateway Webhooks settings.
4. Remove the app's `Sites.Selected` site grant via Microsoft 365 admin.
5. Keep the `teams-bot-data` volume for audit until removal is explicitly authorized.

The base gateway and all other services remain unaffected.

---

## Remaining before go-live

| Item | Status |
|---|---|
| Local typecheck + 20/20 tests | ✅ |
| Docker image builds | ✅ |
| Compose overlay validates | ✅ |
| Azure Bot MsTeamsChannel enabled | ✅ |
| Teams app package generated | ✅ (`teams-app.zip`, replace `TEAMS_APP_HOST`) |
| App package uploaded to Teams Admin | ⬜ |
| SharePoint `Sites.Selected` grant | ⬜ |
| Dedicated webhook agent configured (no write/delegation tools) | ⬜ |
| Actor/project session isolation verified in real gateway | ⬜ |
| Public HTTPS endpoint + `/api/messages` routing | ⬜ |
| Bot mentioned in approved channel (route registered) | ⬜ |
| Per-user MCP credentials issued and installed | ⬜ |
| Live Teams mention → agent reply smoke test | ⬜ |
| Live MCP `post_assignment` → Teams channel smoke test | ⬜ |

---

## Security notes

- **Direct posting:** configured actors may post within their project/destination
  policy without per-assignment approval. Untrusted document text can influence a
  permitted post (prompt injection). Tool descriptions do not eliminate this risk.
- **Port 3979:** not published externally but reachable by all Docker bridge peers.
  Bearer authentication is required on every MCP request regardless.
- **State directory:** `chmod 700`, owned by the service UID. The store constructor
  refuses to open a world-readable path.
- **Secrets:** never in `docker compose config` output, logs, or model arguments.
  Webhook secrets are read from environment variables named in `config.json`, not
  stored in the config file itself.
- **`delivery_unknown`:** means the send timed out before a receipt was confirmed.
  Never treat it as success. Operator reconciliation required before any retry.
