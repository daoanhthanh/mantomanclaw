import { useState } from "react";
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
import type { CreateWorkstationParams, WorkstationBackendType } from "./hooks/use-workstations";
import {
  buildWorkstationCreatePayload,
  type SshAuthMethod,
} from "./workstation-create-dialog-helpers";

interface WorkstationCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreate: (params: CreateWorkstationParams) => Promise<void>;
}

export function WorkstationCreateDialog({
  open,
  onOpenChange,
  onCreate,
}: WorkstationCreateDialogProps) {
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

  const [submitting, setSubmitting] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);

  function resetForm() {
    setName("");
    setKey("");
    setBackend("ssh");
    setHost("");
    setPort("22");
    setUser("");
    setAuthMethod("privateKey");
    setPrivateKeyFile(null);
    setPassword("");
    setContainer("");
    setImage("");
    setSocketPath("");
    setFieldError(null);
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
      jumpEnabled, jumpHost, jumpPort, jumpUser, jumpAuthMethod,
      jumpPrivateKey, jumpPassword, jumpFingerprint,
      container, image, socketPath,
    });
    if (built.kind === "error") {
      setFieldError(t(`createDialog.errors.${built.errorKey}`));
      setSubmitting(false);
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
            <DialogTitle>{t("createDialog.title")}</DialogTitle>
            <DialogDescription>{t("createDialog.description")}</DialogDescription>
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
                onChange={(e) => setKey(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ""))}
                placeholder={t("createDialog.keyPlaceholder")}
                required
                className="text-base md:text-sm"
              />
              <p className="text-xs text-muted-foreground">{t("createDialog.keyHint")}</p>
            </div>

            <div className="space-y-1.5">
              <Label>{t("createDialog.backendLabel")}</Label>
              <Select value={backend} onValueChange={(v) => setBackend(v as WorkstationBackendType)}>
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
                        {privateKeyFile?.name ?? t("createDialog.privateKeyFileHint")}
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
                    <Input
                      id="ws-password"
                      type="password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      placeholder={t("createDialog.passwordPlaceholder")}
                      autoComplete="new-password"
                      className="text-base md:text-sm"
                    />
                    <p className="text-xs text-muted-foreground">{t("createDialog.passwordHint")}</p>
                  </div>
                )}
              </>
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

          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => { resetForm(); onOpenChange(false); }} disabled={submitting}>
              {t("createDialog.cancel")}
            </Button>
            <Button type="submit" disabled={submitting || !name.trim() || !key.trim()}>
              {t("createDialog.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
