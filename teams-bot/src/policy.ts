import type { ActorConfig, FileReference, PolicyResult, ProjectConfig, RecipientConfig, ServiceConfig } from "./contracts.js";

export type AuthorizedActor = { actor: ActorConfig; credentialId?: string };

export function findActor(config: ServiceConfig, actorId: string): ActorConfig | undefined {
  return config.actors.find((actor) => actor.id === actorId);
}

export function findActorByEntra(config: ServiceConfig, entraObjectId: string): ActorConfig | undefined {
  return config.actors.find((actor) => actor.entraObjectId === entraObjectId);
}

export function findProject(config: ServiceConfig, projectIdOrAlias: string): ProjectConfig | undefined {
  const key = projectIdOrAlias.toLowerCase();
  return config.projects.find((project) => project.id.toLowerCase() === key || project.aliases.some((alias) => alias.toLowerCase() === key));
}

export function findProjectByChannel(config: ServiceConfig, teamId: string, channelId: string): ProjectConfig | undefined {
  return config.projects.find((project) => project.destination.teamId === teamId && project.destination.channelId === channelId);
}

export function authorizeActorProject(config: ServiceConfig, actorId: string, projectId: string): PolicyResult<{ actor: ActorConfig; project: ProjectConfig }> {
  const actor = findActor(config, actorId);
  if (!actor) return deny("unknown_actor", "actor is not configured");
  const project = findProject(config, projectId);
  if (!project) return deny("unknown_project", "project is not configured");
  if (!project.actors.includes(actor.id)) return deny("actor_not_permitted", "actor is not permitted for this project");
  return { ok: true, value: { actor, project } };
}

export function authorizeDestination(project: ProjectConfig, teamId: string, channelId: string): PolicyResult<ProjectConfig> {
  if (project.destination.teamId !== teamId || project.destination.channelId !== channelId) {
    return deny("unknown_destination", "destination is not permitted for this project");
  }
  return { ok: true, value: project };
}

export function resolveRecipient(project: ProjectConfig, input: { alias?: string; entraObjectId?: string; teamsUserId?: string }): PolicyResult<RecipientConfig> {
  const matches = project.recipients.filter((recipient) => {
    if (input.alias && recipient.alias === input.alias.toLowerCase()) return true;
    if (input.entraObjectId && recipient.entraObjectId === input.entraObjectId) return true;
    if (input.teamsUserId && recipient.teamsUserId === input.teamsUserId) return true;
    return false;
  });
  const unique = dedupeRecipients(matches);
  if (unique.length === 0) return deny("unknown_recipient", "recipient is not in the project directory");
  if (unique.length > 1) return deny("ambiguous_recipient", "recipient alias matches more than one person");
  const recipient = unique[0];
  if (!recipient) return deny("unknown_recipient", "recipient is not in the project directory");
  return { ok: true, value: recipient };
}

export function authorizeFile(project: ProjectConfig, file: FileReference, resolved?: { siteId?: string; driveId?: string; path?: string; host?: string }): PolicyResult<FileReference> {
  if (file.kind === "sharepoint-url") {
    if (!file.url) return deny("file_outside_scope", "file URL is missing");
    let url: URL;
    try {
      url = new URL(file.url);
    } catch {
      return deny("file_outside_scope", "file URL is invalid");
    }
    if (url.protocol !== "https:") return deny("file_outside_scope", "file URL must be https");
    if (!project.allowedHosts.includes(url.hostname)) return deny("file_outside_scope", "file host is not an approved library host");
  }
  if (resolved && (!resolved.siteId || !resolved.driveId || !resolved.path || !resolved.host)) {
    return deny("file_outside_scope", "resolved file metadata is incomplete");
  }
  if (resolved?.host && !project.allowedHosts.includes(resolved.host)) return deny("file_outside_scope", "resolved host is outside the approved library");
  if (resolved?.siteId && resolved.siteId !== project.siteId) return deny("file_outside_scope", "resolved site is outside the approved library");
  if (resolved?.driveId && resolved.driveId !== project.driveId) return deny("file_outside_scope", "resolved drive is outside the approved library");
  if (file.driveId && file.driveId !== project.driveId) return deny("file_outside_scope", "drive is outside the approved library");
  if (resolved?.path && !pathInLibrary(resolved.path, project.libraryPathPrefix)) {
    return deny("file_outside_scope", "resolved path is outside the approved library");
  }
  return { ok: true, value: file };
}

export function pathInLibrary(path: string, prefix: string): boolean {
  const root = path.indexOf("root:");
  if (root < 0) return false;
  const relative = path.slice(root + 5).replace(/^\/+|\/+$/g, "").toLowerCase();
  const wanted = prefix.replace(/^\/+|\/+$/g, "").toLowerCase();
  if (relative.split("/").some((segment) => segment === "." || segment === "..")) return false;
  return wanted === "" || relative === wanted || relative.startsWith(`${wanted}/`);
}

function dedupeRecipients(recipients: RecipientConfig[]): RecipientConfig[] {
  const seen = new Set<string>();
  const out: RecipientConfig[] = [];
  for (const recipient of recipients) {
    if (seen.has(recipient.entraObjectId)) continue;
    seen.add(recipient.entraObjectId);
    out.push(recipient);
  }
  return out;
}

function deny(code: Extract<PolicyResult<never>, { ok: false }>["code"], detail: string): Extract<PolicyResult<never>, { ok: false }> {
  return { ok: false, code, detail };
}
