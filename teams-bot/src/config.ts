import { readFileSync } from "node:fs";
import {
  DEFAULT_GATEWAY_TIMEOUT_SEC,
  DEFAULT_HTTP_DEADLINE_SEC,
  type ActorConfig,
  type ProjectConfig,
  type ServiceConfig,
} from "./contracts.js";

const ID_RE = /^[A-Za-z0-9_.:@,;!-]{1,300}$/;
const DISPLAY_RE = /^[^<>&]{1,120}$/;

export class ConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConfigError";
  }
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): ServiceConfig {
  const path = env.TEAMS_BOT_CONFIG;
  if (!path) throw new ConfigError("TEAMS_BOT_CONFIG is required");
  const parsed = JSON.parse(readFileSync(path, "utf8")) as unknown;
  return loadConfigFromObject(parsed, env);
}

export function loadConfigFromObject(raw: unknown, env: NodeJS.ProcessEnv = process.env): ServiceConfig {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new ConfigError("config must be an object");
  const body = raw as Record<string, unknown>;
  const gatewaySyncTimeoutSec = intField(
    env.MANTOMANAGENT_WEBHOOK_SYNC_TIMEOUT_SEC ?? body.gatewaySyncTimeoutSec,
    DEFAULT_GATEWAY_TIMEOUT_SEC,
    "gatewaySyncTimeoutSec",
  );
  const httpDeadlineSec = intField(
    env.TEAMS_BOT_HTTP_DEADLINE_SEC ?? body.httpDeadlineSec,
    DEFAULT_HTTP_DEADLINE_SEC,
    "httpDeadlineSec",
  );
  if (httpDeadlineSec <= gatewaySyncTimeoutSec) {
    throw new ConfigError("httpDeadlineSec must exceed the deployed gateway sync timeout");
  }
  const config: ServiceConfig = {
    installationId: requireId(env.TEAMS_BOT_INSTALLATION_ID ?? body.installationId, "installationId"),
    microsoftTenantId: requireId(env.MICROSOFT_TENANT_ID ?? body.microsoftTenantId, "microsoftTenantId"),
    mantomanTenantId: requireId(env.MANTOMAN_TENANT_ID ?? body.mantomanTenantId, "mantomanTenantId"),
    mantomanBaseUrl: requireHttpUrl(env.MANTOMAN_BASE_URL ?? body.mantomanBaseUrl, "mantomanBaseUrl"),
    clientId: requireId(env.MICROSOFT_CLIENT_ID ?? body.clientId, "clientId"),
    clientSecret: requireSecret(env.MICROSOFT_CLIENT_SECRET ?? body.clientSecret, "clientSecret"),
    teamsServiceUrl: requireHttpUrl(
      env.TEAMS_SERVICE_URL ?? body.teamsServiceUrl ?? "https://smba.trafficmanager.net/teams",
      "teamsServiceUrl",
    ),
    statePath: requireString(env.TEAMS_BOT_STATE_PATH ?? body.statePath, "statePath"),
    publicPort: intField(env.TEAMS_BOT_PUBLIC_PORT ?? body.publicPort, 3978, "publicPort"),
    privatePort: intField(env.TEAMS_BOT_PRIVATE_PORT ?? body.privatePort, 3979, "privatePort"),
    gatewaySyncTimeoutSec,
    httpDeadlineSec,
    mcpAllowedOrigins: stringList(body.mcpAllowedOrigins),
    mcpAllowedHosts: stringList(body.mcpAllowedHosts, ["127.0.0.1", "localhost", "teams-bot"]),
    actors: parseActors(body.actors),
    projects: parseProjects(body.projects, env),
  };
  if (config.publicPort === config.privatePort) throw new ConfigError("public and private ports must differ");
  const actorIds = new Set(config.actors.map((actor) => actor.id));
  if (actorIds.size !== config.actors.length || new Set(config.actors.map((actor) => actor.entraObjectId)).size !== config.actors.length) {
    throw new ConfigError("actor IDs and Entra object IDs must be unique");
  }
  const projectNames = new Map<string, string>();
  if (new Set(config.projects.map((project) => project.id.toLowerCase())).size !== config.projects.length) {
    throw new ConfigError("project IDs must be unique");
  }
  const agents = new Set<string>();
  const webhooks = new Set<string>();
  const destinations = new Set<string>();
  for (const project of config.projects) {
    for (const name of [project.id, ...project.aliases]) {
      const key = name.toLowerCase();
      const owner = projectNames.get(key);
      if (owner && owner !== project.id) throw new ConfigError("project IDs and aliases must be unambiguous");
      projectNames.set(key, project.id);
    }
    const destination = JSON.stringify([project.destination.teamId, project.destination.channelId]);
    if (agents.has(project.agentId) || webhooks.has(project.webhookId) || destinations.has(destination)) {
      throw new ConfigError("projects must have distinct agents, webhooks, and destinations");
    }
    agents.add(project.agentId);
    webhooks.add(project.webhookId);
    destinations.add(destination);
    for (const actorId of project.actors) {
      if (!actorIds.has(actorId)) throw new ConfigError(`project ${project.id} references unknown actor ${actorId}`);
    }
  }
  return config;
}

function parseActors(value: unknown): ActorConfig[] {
  if (!Array.isArray(value) || value.length === 0) throw new ConfigError("actors are required");
  return value.map((item, index) => {
    const row = objectAt(item, `actors[${index}]`);
    return {
      id: requireId(row.id, `actors[${index}].id`),
      mantomanUserId: requireId(row.mantomanUserId, `actors[${index}].mantomanUserId`),
      entraObjectId: requireId(row.entraObjectId, `actors[${index}].entraObjectId`),
      displayName: requireDisplay(row.displayName, `actors[${index}].displayName`),
    };
  });
}

function parseProjects(value: unknown, env: NodeJS.ProcessEnv): ProjectConfig[] {
  if (!Array.isArray(value) || value.length === 0) throw new ConfigError("projects are required");
  return value.map((item, index) => {
    const row = objectAt(item, `projects[${index}]`);
    const destination = objectAt(row.destination, `projects[${index}].destination`);
    const recipients = Array.isArray(row.recipients) ? row.recipients : [];
    return {
      id: requireId(row.id, `projects[${index}].id`),
      aliases: stringList(row.aliases, [requireId(row.id, `projects[${index}].id`)]),
      agentId: requireId(row.agentId, `projects[${index}].agentId`),
      webhookId: requireId(row.webhookId, `projects[${index}].webhookId`),
      webhookSecret: requireSecret(env[requireString(row.webhookSecretEnv, `projects[${index}].webhookSecretEnv`)], `projects[${index}].webhookSecretEnv`),
      siteId: requireId(row.siteId, `projects[${index}].siteId`),
      driveId: requireId(row.driveId, `projects[${index}].driveId`),
      libraryPathPrefix: requireString(row.libraryPathPrefix, `projects[${index}].libraryPathPrefix`),
      allowedHosts: stringList(row.allowedHosts),
      actors: stringList(row.actors),
      destination: {
        teamId: requireId(destination.teamId, `projects[${index}].destination.teamId`),
        channelId: requireId(destination.channelId, `projects[${index}].destination.channelId`),
      },
      recipients: recipients.map((recipient, recipientIndex) => {
        const person = objectAt(recipient, `projects[${index}].recipients[${recipientIndex}]`);
        return {
          alias: requireId(person.alias, `recipient.alias`).toLowerCase(),
          entraObjectId: requireId(person.entraObjectId, `recipient.entraObjectId`),
          teamsUserId: requireId(person.teamsUserId, `recipient.teamsUserId`),
          displayName: requireDisplay(person.displayName, `recipient.displayName`),
        };
      }),
    };
  });
}

function objectAt(value: unknown, name: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ConfigError(`${name} must be an object`);
  return value as Record<string, unknown>;
}

function requireString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim() === "") throw new ConfigError(`${name} is required`);
  return value.trim();
}

function requireId(value: unknown, name: string): string {
  const text = requireString(value, name);
  if (!ID_RE.test(text)) throw new ConfigError(`${name} has an invalid identifier`);
  return text;
}

function requireSecret(value: unknown, name: string): string {
  const text = requireString(value, name);
  if (text.length < 16) throw new ConfigError(`${name} is too short`);
  return text;
}

function requireDisplay(value: unknown, name: string): string {
  const text = requireString(value, name);
  if (!DISPLAY_RE.test(text)) throw new ConfigError(`${name} contains markup characters`);
  return text;
}

function requireHttpUrl(value: unknown, name: string): string {
  const text = requireString(value, name);
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    throw new ConfigError(`${name} must be an absolute URL`);
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") throw new ConfigError(`${name} must be http(s)`);
  return text.replace(/\/$/, "");
}

function intField(value: unknown, fallback: number, name: string): number {
  if (value === undefined || value === "") return fallback;
  const parsed = typeof value === "number" ? value : Number(value);
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 86400) throw new ConfigError(`${name} is invalid`);
  return parsed;
}

function stringList(value: unknown, fallback: string[] = []): string[] {
  if (value === undefined) return fallback;
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string" || item.trim() === "")) {
    throw new ConfigError("expected a string list");
  }
  return value.map((item) => item.trim());
}
