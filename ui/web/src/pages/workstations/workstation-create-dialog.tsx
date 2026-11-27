import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { CloudUpload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { CreateWorkstationParams, UpdateWorkstationParams, Workstation, WorkstationBackendType } from "./hooks/use-workstations";
import {
  buildWorkstationCreatePayload,
  buildWorkstationUpdatePayload,
  type SshAuthMethod,
} from "./workstation-create-dialog-helpers";

interface WorkstationCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreate: (params: CreateWorkstationParams) => Promise<void>;
  /** When set, the dialog is in edit mode — fields are pre-filled and key/backend are locked. */
  editTarget?: Workstation | null;
  onUpdate?: (id: string, params: UpdateWorkstationParams) => Promise<void>;
  onTest?: (id: string) => Promise<void>;
}

export function WorkstationCreateDialog({
  open,
  onOpenChange,
  onCreate,
  editTarget,
  onUpdate,
  onTest,
}: WorkstationCreateDialogProps) {
  const isEdit = !!editTarget;
  const { t } = useTranslation("workstations");

  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [backend, setBackend] = useState<WorkstationBackendType>("ssh");
  // SSH fields
  const [host, setHost] = useState("");
  const [port, setPort] = useState("22");
  const [user, setUser] = useState("");
  const [authMethod, setAuthMethod] = useState<SshAuthMethod>("privateKey");
  const [privateKeyFile, setPrivateKeyFile] = useState<File | null>(null);
  const [password, setPassword] = useState("");
  // SSH jump host
  const [jumpEnabled, setJumpEnabled] = useState(false);
  const [jumpHost, setJumpHost] = useState("");
  const [jumpPort, setJumpPort] = useState("22");
  const [jumpUser, setJumpUser] = useState("");
  const [jumpAuthMethod, setJumpAuthMethod] = useState<SshAuthMethod>("privateKey");
  const [jumpPrivateKeyFile, setJumpPrivateKeyFile] = useState<File | null>(null);
  const [jumpPassword, setJumpPassword] = useState("");
  const [jumpFingerprint, setJumpFingerprint] = useState("");
  // Docker fields
  const [container, setContainer] = useState("");
  const [image, setImage] = useState("");
  const [socketPath, setSocketPath] = useState("");
  const [knownHostsFingerprint, setKnownHostsFingerprint] = useState("");
  const [connectTimeoutSec, setConnectTimeoutSec] = useState("");

  const [submitting, setSubmitting] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; msg: string } | null>(null);
  const [fieldError, setFieldError] = useState<string | null>(null);

  function resetForm() {
    setName(""); setKey(""); setBackend("ssh");
    setHost(""); setPort("22"); setUser(""); setAuthMethod("privateKey");
    setPrivateKeyFile(null); setPassword(""); setKnownHostsFingerprint(""); setConnectTimeoutSec("");
    setJumpEnabled(false); setJumpHost(""); setJumpPort("22"); setJumpUser("");
    setJumpAuthMethod("privateKey"); setJumpPrivateKeyFile(null);
    setJumpPassword(""); setJumpFingerprint("");
    setContainer(""); setImage(""); setSocketPath("");
    setFieldError(null);
    setTestResult(null);
  }

  // Pre-fill form from editTarget whenever it changes.
  useEffect(() => {
    if (!editTarget) return;
    const ms = editTarget.metadataSummary;
    setName(editTarget.name);
    setKey(editTarget.workstationKey);
    setBackend(editTarget.backendType);
    setFieldError(null);
    setTestResult(null);
    if (editTarget.backendType === "ssh" && ms) {
      setHost(String(ms.host ?? ""));
      setPort(String(ms.port ?? "22"));
      setUser(String(ms.user ?? ""));
      setKnownHostsFingerprint(String(ms.knownHostsFingerprint ?? ""));
      setConnectTimeoutSec(ms.connectTimeoutSec ? String(ms.connectTimeoutSec) : "");
      setAuthMethod(ms.hasKey ? "privateKey" : "password");
      setPrivateKeyFile(null); setPassword("");
      if (ms.jumpHost) {
        setJumpEnabled(true);
        setJumpHost(ms.jumpHost.host ?? "");
        setJumpPort(String(ms.jumpHost.port ?? "22"));
        setJumpUser(ms.jumpHost.user ?? "");
        setJumpAuthMethod(ms.jumpHost.hasKey ? "privateKey" : "password");
        setJumpFingerprint(ms.jumpHost.knownHostsFingerprint ?? "");
        setJumpPrivateKeyFile(null); setJumpPassword("");
      } else {
        setJumpEnabled(false);
      }
    } else if (editTarget.backendType === "docker" && ms) {
      setContainer(String(ms.containerName ?? ""));
      setImage(String(ms.image ?? ""));
      setSocketPath(String(ms.socketPath ?? ""));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editTarget]);

  async function handleTest() {
    if (!editTarget || !onTest || testing) return;
    setTesting(true);
    setTestResult(null);
    try {
      await onTest(editTarget.id);
      setTestResult({ ok: true, msg: t("testResult.success") });
    } catch (err) {
      setTestResult({ ok: false, msg: err instanceof Error ? err.message : t("testResult.failed") });
    } finally {
      setTesting(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    let privateKey = "";
    if (backend === "ssh" && authMethod === "privateKey" && privateKeyFile) {
      try { privateKey = await privateKeyFile.text(); }
      catch { setFieldError(t("createDialog.errors.privateKeyReadFailed")); setSubmitting(false); return; }
    }
    let jumpPrivateKey = "";
    if (backend === "ssh" && jumpEnabled && jumpAuthMethod === "privateKey" && jumpPrivateKeyFile) {
      try { jumpPrivateKey = await jumpPrivateKeyFile.text(); }
      catch { setFieldError(t("createDialog.errors.privateKeyReadFailed")); setSubmitting(false); return; }
    }

    const built = buildWorkstationCreatePayload({
      key, name, backend,
      host, port, user, authMethod, privateKey, password,
      knownHostsFingerprint, connectTimeoutSec,
      jumpEnabled, jumpHost, jumpPort, jumpUser, jumpAuthMethod,
      jumpPrivateKey, jumpPassword, jumpFingerprint,
      container, image, socketPath,
    });
    if (built.kind === "error") {
      setFieldError(t(`createDialog.errors.${built.errorKey}`));
      setSubmitting(false);
      return;
    }

    if (isEdit && editTarget && onUpdate) {
      const ms = editTarget.metadataSummary;
      const updateBuilt = buildWorkstationUpdatePayload(
        { key, name, backend, host, port, user, authMethod, privateKey, password,
          knownHostsFingerprint, connectTimeoutSec,
          jumpEnabled, jumpHost, jumpPort, jumpUser, jumpAuthMethod,
          jumpPrivateKey, jumpPassword, jumpFingerprint,
          container, image, socketPath },
        {
          hasKey: ms?.hasKey,
          hasPassword: ms?.hasPassword,
          kfp: ms?.knownHostsFingerprint,
          jumpHasKey: ms?.jumpHost?.hasKey,
          jumpHasPassword: ms?.jumpHost?.hasPassword,
          jumpKfp: ms?.jumpHost?.knownHostsFingerprint,
        },
      );
      if (updateBuilt.kind === "error") {
        setFieldError(t(`createDialog.errors.${updateBuilt.errorKey}`));
        setSubmitting(false);
        return;
      }
      setFieldError(null);
      setSubmitting(true);
      try {
        await onUpdate(editTarget.id, updateBuilt.payload);
        onOpenChange(false);
      } catch (err) {
        setFieldError(err instanceof Error ? err.message : t("editDialog.saveFailed"));
      } finally {
        setSubmitting(false);
      }
      return;
    }

    setFieldError(null);
    setSubmitting(true);
    try {
      await onCreate(built.payload);
      resetForm();
      onOpenChange(false);
    } catch (err) {
      setFieldError(err instanceof Error ? err.message : t("createDialog.errors.createFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!submitting) { resetForm(); onOpenChange(v); } }}>
      <DialogContent
        className="max-h-dvh overflow-y-auto overscroll-contain sm:max-h-[calc(100dvh-2rem)] sm:max-w-lg"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>{isEdit ? t("editDialog.title") : t("createDialog.title")}</DialogTitle>
            <DialogDescription>{isEdit ? t("editDialog.description") : t("createDialog.description")}</DialogDescription>
          </DialogHeader>

          <div className="mt-4 space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="ws-name">{t("createDialog.nameLabel")}</Label>
              <Input
                id="ws-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={t("createDialog.namePlaceholder")}
                required
                className="text-base md:text-sm"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="ws-key">{t("createDialog.keyLabel")}</Label>
              <Input
                id="ws-key"
                value={key}
                readOnly={isEdit}
                onChange={(e) => { if (!isEdit) setKey(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "")); }}
                placeholder={t("createDialog.keyPlaceholder")}
                required
                className="text-base md:text-sm"
              />
              <p className="text-xs text-muted-foreground">{t("createDialog.keyHint")}</p>
            </div>

            <div className="space-y-1.5">
              <Label>{t("createDialog.backendLabel")}</Label>
              <Select value={backend} onValueChange={(v) => { if (!isEdit) setBackend(v as WorkstationBackendType); }} disabled={isEdit}>
                <SelectTrigger className="text-base md:text-sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="ssh">{t("createDialog.sshOption")}</SelectItem>
                  <SelectItem value="docker">{t("createDialog.dockerOption")}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {backend === "ssh" && (
              <>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <div className="space-y-1.5 sm:col-span-2">
                    <Label htmlFor="ws-host">{t("createDialog.hostLabel")}</Label>
                    <Input
                      id="ws-host"
                      value={host}
                      onChange={(e) => setHost(e.target.value)}
                      placeholder={t("createDialog.hostPlaceholder")}
                      className="text-base md:text-sm"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="ws-port">{t("createDialog.portLabel")}</Label>
                    <Input
                      id="ws-port"
                      type="number"
                      min={1}
                      max={65535}
                      value={port}
                      onChange={(e) => setPort(e.target.value)}
                      className="text-base md:text-sm"
                    />
                  </div>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="ws-user">{t("createDialog.userLabel")}</Label>
                  <Input
                    id="ws-user"
                    value={user}
                    onChange={(e) => setUser(e.target.value)}
                    placeholder={t("createDialog.userPlaceholder")}
                    className="text-base md:text-sm"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label>{t("createDialog.authMethodLabel")}</Label>
                  <Select value={authMethod} onValueChange={(v) => setAuthMethod(v as SshAuthMethod)}>
                    <SelectTrigger className="text-base md:text-sm">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="privateKey">{t("createDialog.authPrivateKeyOption")}</SelectItem>
                      <SelectItem value="password">{t("createDialog.authPasswordOption")}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                {authMethod === "privateKey" ? (
                  <div className="space-y-1.5">
                    <Label htmlFor="ws-private-key">{t("createDialog.privateKeyLabel")}</Label>
                    {isEdit && editTarget?.metadataSummary?.hasKey && !privateKeyFile && (
                      <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-300">
                        <span>🔑</span>
                        <span>{t("editDialog.keyStored")}</span>
                      </div>
                    )}
                    <div
                      className={`relative flex flex-col items-center gap-3 rounded-2xl border border-border px-4 py-6 text-center transition-colors focus-within:ring-2 focus-within:ring-ring ${submitting ? "opacity-50" : "hover:border-primary/50 hover:bg-muted/30"}`}
                      onDragOver={(e) => e.preventDefault()}
                      onDrop={(e) => {
                        e.preventDefault();
                        if (!submitting && e.dataTransfer.files.length === 1) {
                          setPrivateKeyFile(e.dataTransfer.files.item(0));
                          setFieldError(null);
                        }
                      }}
                    >
                      <div className="rounded-full bg-muted/50 p-3 ring-8 ring-muted/20">
                        <CloudUpload className="size-6 text-muted-foreground" aria-hidden="true" />
                      </div>
                      <p className="text-base">
                        <span className="font-semibold text-blue-600 dark:text-blue-400">{t("createDialog.privateKeyUpload")}</span>{" "}
                        <span>{t("createDialog.privateKeyDropHint")}</span>
                      </p>
                      <p className="max-w-full break-all text-sm text-muted-foreground" aria-live="polite">
                        {privateKeyFile?.name ?? (isEdit && editTarget?.metadataSummary?.hasKey ? t("editDialog.keyReplace") : t("createDialog.privateKeyFileHint"))}
                      </p>
                      <Input
                        id="ws-private-key"
                        type="file"
                        disabled={submitting}
                        aria-describedby="ws-private-key-hint"
                        onChange={(e) => {
                          const file = e.target.files?.[0];
                          if (file) {
                            setPrivateKeyFile(file);
                            setFieldError(null);
                          }
                          e.target.value = "";
                        }}
                        className="absolute inset-0 h-full w-full cursor-pointer opacity-0 disabled:cursor-not-allowed"
                      />
                    </div>
                    <p id="ws-private-key-hint" className="text-xs text-muted-foreground">{t("createDialog.privateKeyHint")}</p>
                  </div>
                ) : (
                  <div className="space-y-1.5">
                    <Label htmlFor="ws-password">{t("createDialog.passwordLabel")}</Label>
                    {isEdit && editTarget?.metadataSummary?.hasPassword && (
                      <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-300">
                        <span>🔑</span>
                        <span>{t("editDialog.passwordStored")}</span>
                      </div>
                    )}
                    <Input
                      id="ws-password"
                      type="password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      placeholder={isEdit && editTarget?.metadataSummary?.hasPassword ? t("editDialog.credentialKeepPlaceholder") : t("createDialog.passwordPlaceholder")}
                      autoComplete="new-password"
                      className="text-base md:text-sm"
                    />
                    <p className="text-xs text-muted-foreground">{t("createDialog.passwordHint")}</p>
                  </div>
                )}
              </>
            )}
            {backend === "ssh" && (
              <details className="group rounded-lg border border-border" open={jumpEnabled}>
                <summary
                  className="flex cursor-pointer select-none items-center justify-between px-3 py-2 text-sm font-medium text-muted-foreground hover:text-foreground"
                  onClick={(e) => { e.preventDefault(); setJumpEnabled((v) => !v); }}
                >
                  {t("createDialog.jumpHostLabel")}
                  <span className="ml-2 text-xs opacity-60">{t("createDialog.jumpHostOptional")}</span>
                </summary>
                {jumpEnabled && (
                  <div className="border-t border-border px-3 pb-3 pt-3 space-y-3">
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                      <div className="space-y-1.5 sm:col-span-2">
                        <Label htmlFor="ws-jump-host">{t("createDialog.jumpHostHostLabel")}</Label>
                        <Input id="ws-jump-host" value={jumpHost} onChange={(e) => setJumpHost(e.target.value)}
                          placeholder="bastion.example.com" className="text-base md:text-sm" />
                      </div>
                      <div className="space-y-1.5">
                        <Label htmlFor="ws-jump-port">{t("createDialog.portLabel")}</Label>
                        <Input id="ws-jump-port" type="number" min={1} max={65535} value={jumpPort}
                          onChange={(e) => setJumpPort(e.target.value)} className="text-base md:text-sm" />
                      </div>
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor="ws-jump-user">{t("createDialog.jumpHostUserLabel")}</Label>
                      <Input id="ws-jump-user" value={jumpUser} onChange={(e) => setJumpUser(e.target.value)}
                        placeholder="ubuntu" className="text-base md:text-sm" />
                    </div>
                    <div className="space-y-1.5">
                      <Label>{t("createDialog.authMethodLabel")}</Label>
                      <Select value={jumpAuthMethod} onValueChange={(v) => setJumpAuthMethod(v as SshAuthMethod)}>
                        <SelectTrigger className="text-base md:text-sm"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="privateKey">{t("createDialog.authPrivateKeyOption")}</SelectItem>
                          <SelectItem value="password">{t("createDialog.authPasswordOption")}</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    {jumpAuthMethod === "privateKey" ? (
                      <div className="space-y-1.5">
                        <Label htmlFor="ws-jump-key">{t("createDialog.privateKeyLabel")}</Label>
                        {isEdit && editTarget?.metadataSummary?.jumpHost?.hasKey && !jumpPrivateKeyFile && (
                          <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-300">
                            <span>🔑</span>
                            <span>{t("editDialog.keyStored")}</span>
                          </div>
                        )}
                        <div className={`relative flex flex-col items-center gap-3 rounded-2xl border border-border px-4 py-6 text-center transition-colors focus-within:ring-2 focus-within:ring-ring ${submitting ? "opacity-50" : "hover:border-primary/50 hover:bg-muted/30"}`}
                          onDragOver={(e) => e.preventDefault()}
                          onDrop={(e) => { e.preventDefault(); if (!submitting && e.dataTransfer.files.length === 1) { setJumpPrivateKeyFile(e.dataTransfer.files.item(0)); setFieldError(null); } }}>
                          <div className="rounded-full bg-muted/50 p-3 ring-8 ring-muted/20">
                            <CloudUpload className="size-6 text-muted-foreground" aria-hidden="true" />
                          </div>
                          <p className="text-base">
                            <span className="font-semibold text-blue-600 dark:text-blue-400">{t("createDialog.privateKeyUpload")}</span>{" "}
                            <span>{t("createDialog.privateKeyDropHint")}</span>
                          </p>
                          <p className="max-w-full break-all text-sm text-muted-foreground" aria-live="polite">
                            {jumpPrivateKeyFile?.name ?? (isEdit && editTarget?.metadataSummary?.jumpHost?.hasKey ? t("editDialog.keyReplace") : t("createDialog.privateKeyFileHint"))}
                          </p>
                          <Input id="ws-jump-key" type="file" disabled={submitting}
                            onChange={(e) => { const f = e.target.files?.[0]; if (f) { setJumpPrivateKeyFile(f); setFieldError(null); } e.target.value = ""; }}
                            className="absolute inset-0 h-full w-full cursor-pointer opacity-0 disabled:cursor-not-allowed" />
                        </div>
                      </div>
                    ) : (
                      <div className="space-y-1.5">
                        <Label htmlFor="ws-jump-password">{t("createDialog.passwordLabel")}</Label>
                        {isEdit && editTarget?.metadataSummary?.jumpHost?.hasPassword && (
                          <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-300">
                            <span>🔑</span>
                            <span>{t("editDialog.passwordStored")}</span>
                          </div>
                        )}
                        <Input id="ws-jump-password" type="password" value={jumpPassword}
                          onChange={(e) => setJumpPassword(e.target.value)}
                          placeholder={isEdit && editTarget?.metadataSummary?.jumpHost?.hasPassword ? t("editDialog.credentialKeepPlaceholder") : t("createDialog.passwordPlaceholder")}
                          autoComplete="new-password" className="text-base md:text-sm" />
                      </div>
                    )}
                    <div className="space-y-1.5">
                      <Label htmlFor="ws-jump-fp">
                        {t("createDialog.jumpFingerprintLabel")}
                        <span className="ml-1 text-destructive" aria-hidden="true">*</span>
                      </Label>
                      <Input id="ws-jump-fp" value={jumpFingerprint}
                        onChange={(e) => setJumpFingerprint(e.target.value)}
                        placeholder="SHA256:..." className="text-base md:text-sm font-mono" />
                      <p className="text-xs text-muted-foreground">{t("createDialog.jumpFingerprintHint")}</p>
                    </div>
                  </div>
                )}
              </details>
            )}


            {backend === "docker" && (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor="ws-container">{t("createDialog.containerLabel")}</Label>
                  <Input
                    id="ws-container"
                    value={container}
                    onChange={(e) => setContainer(e.target.value)}
                    placeholder={t("createDialog.containerPlaceholder")}
                    className="text-base md:text-sm"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="ws-image">{t("createDialog.imageLabel")}</Label>
                  <Input
                    id="ws-image"
                    value={image}
                    onChange={(e) => setImage(e.target.value)}
                    placeholder={t("createDialog.imagePlaceholder")}
                    className="text-base md:text-sm"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="ws-socket-path">{t("createDialog.socketPathLabel")}</Label>
                  <Input
                    id="ws-socket-path"
                    value={socketPath}
                    onChange={(e) => setSocketPath(e.target.value)}
                    placeholder={t("createDialog.socketPathPlaceholder")}
                    className="text-base md:text-sm"
                  />
                </div>
              </>
            )}

            {fieldError && (
              <p className="text-sm text-destructive">{fieldError}</p>
            )}
          </div>

          {testResult && (
            <div className={`mt-4 flex items-center gap-2 rounded-lg border px-3 py-2 text-sm ${testResult.ok ? "border-green-200 bg-green-50 text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-300" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
              <span>{testResult.ok ? "✓" : "✗"}</span>
              <span>{testResult.msg}</span>
            </div>
          )}

          <DialogFooter className="mt-4">
            {isEdit && onTest && (
              <Button type="button" variant="outline" onClick={handleTest} disabled={submitting || testing} className="mr-auto">
                {testing ? t("editDialog.testing") : t("editDialog.testConnection")}
              </Button>
            )}
            <Button type="button" variant="outline" onClick={() => { resetForm(); onOpenChange(false); }} disabled={submitting || testing}>
              {t("createDialog.cancel")}
            </Button>
            <Button type="submit" disabled={submitting || testing || !name.trim() || !key.trim()}>
              {isEdit ? t("editDialog.save") : t("createDialog.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
