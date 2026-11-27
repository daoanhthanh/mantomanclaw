import type { CreateWorkstationParams, WorkstationBackendType } from "./hooks/use-workstations";

/** Auth method for the SSH backend. The gateway accepts inline PEM or a password. */
export type SshAuthMethod = "privateKey" | "password";

export interface WorkstationCreateFormState {
  key: string;
  name: string;
  backend: WorkstationBackendType;
  // SSH target
  host: string;
  port: string;
  user: string;
  authMethod: SshAuthMethod;
  privateKey: string;
  password: string;
  // SSH jump host
  jumpEnabled: boolean;
  jumpHost: string;
  jumpPort: string;
  jumpUser: string;
  jumpAuthMethod: SshAuthMethod;
  jumpPrivateKey: string;
  jumpPassword: string;
  jumpFingerprint: string;
  // Docker
  container: string;
  image: string;
  socketPath: string;
}

export type BuildCreatePayloadResult =
  | { kind: "ok"; payload: CreateWorkstationParams }
  | { kind: "error"; errorKey: string };

/**
 * Builds the workstations.create RPC payload from dialog form state.
 *
 * Field names here must match the gateway wire contract exactly — the WS client
 * serializes params verbatim with no case conversion, and Go's json decoder
 * silently drops unknown members rather than erroring. A mismatched name
 * therefore surfaces as "<field> is required" rather than as a decode failure.
 *
 * Metadata shape mirrors store.SSHMetadata / store.DockerMetadata: SSH requires
 * privateKey or password, and Docker requires an image plus host or socketPath.
 */
export function buildWorkstationCreatePayload(
  form: WorkstationCreateFormState,
): BuildCreatePayloadResult {
  const key = form.key.trim();
  const name = form.name.trim();

  if (!key) return { kind: "error", errorKey: "keyRequired" };
  if (!name) return { kind: "error", errorKey: "nameRequired" };

  let metadata: Record<string, unknown>;

  if (form.backend === "ssh") {
    const host = form.host.trim();
    const user = form.user.trim();
    if (!host || !user) return { kind: "error", errorKey: "sshHostUserRequired" };

    const port = parseInt(form.port, 10) || 22;
    if (port < 1 || port > 65535) return { kind: "error", errorKey: "sshPortRange" };

    const privateKey = form.privateKey.trim();
    const password = form.password;
    if (form.authMethod === "privateKey" && !privateKey) {
      return { kind: "error", errorKey: "sshPrivateKeyRequired" };
    }
    if (form.authMethod === "password" && !password) {
      return { kind: "error", errorKey: "sshPasswordRequired" };
    }

    let jumpMetadata: Record<string, unknown> | undefined;
    if (form.jumpEnabled) {
      const jumpHostVal = form.jumpHost.trim();
      const jumpUserVal = form.jumpUser.trim();
      if (!jumpHostVal || !jumpUserVal) return { kind: "error", errorKey: "jumpHostUserRequired" };
      const jumpPort = parseInt(form.jumpPort, 10) || 22;
      if (jumpPort < 1 || jumpPort > 65535) return { kind: "error", errorKey: "jumpPortRange" };
      if (form.jumpAuthMethod === "privateKey" && !form.jumpPrivateKey.trim()) {
        return { kind: "error", errorKey: "jumpPrivateKeyRequired" };
      }
      if (form.jumpAuthMethod === "password" && !form.jumpPassword) {
        return { kind: "error", errorKey: "jumpPasswordRequired" };
      }
      if (!form.jumpFingerprint.trim()) {
        return { kind: "error", errorKey: "jumpFingerprintRequired" };
      }
      jumpMetadata = {
        host: jumpHostVal,
        port: jumpPort,
        user: jumpUserVal,
        knownHostsFingerprint: form.jumpFingerprint.trim(),
        ...(form.jumpAuthMethod === "privateKey"
          ? { privateKey: form.jumpPrivateKey.trim() }
          : { password: form.jumpPassword }),
      };
    }

    metadata = {
      host,
      port,
      user,
      ...(form.authMethod === "privateKey" ? { privateKey } : { password }),
      ...(jumpMetadata ? { jumpHost: jumpMetadata } : {}),
    };
  } else {
    // The gateway stores the container name in DockerMetadata.host and surfaces
    // it back as metadataSummary.containerName.
    const container = form.container.trim();
    const image = form.image.trim();
    const socketPath = form.socketPath.trim();
    if (!container) return { kind: "error", errorKey: "dockerContainerRequired" };
    if (!image) return { kind: "error", errorKey: "dockerImageRequired" };

    metadata = {
      host: container,
      image,
      ...(socketPath ? { socketPath } : {}),
    };
  }

  return {
    kind: "ok",
    payload: {
      workstationKey: key,
      name,
      backendType: form.backend,
      metadata,
    },
  };
}
