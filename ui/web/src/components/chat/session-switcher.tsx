import { memo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DropdownMenu } from "radix-ui";
import { MessageSquare, MoreHorizontal, Pencil, Pin, Trash2 } from "lucide-react";
import { formatRelativeTime } from "@/lib/format";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { SessionInfo } from "@/types/session";

interface SessionSwitcherProps {
  sessions: SessionInfo[];
  activeKey: string;
  onSelect: (key: string) => void;
  onDelete?: (key: string) => void;
  onRename?: (key: string, title: string) => Promise<boolean>;
  onPin?: (key: string, pinned: boolean) => Promise<boolean>;
  loading?: boolean;
}

/** Build a human-friendly label from session metadata or key */
function sessionLabel(session: SessionInfo): string {
  if (session.metadata?.chat_title) return session.metadata.chat_title;
  if (session.metadata?.display_name) return session.metadata.display_name;
  if (session.label) return session.label;

  const parts = session.key.split(":");
  const scope = parts.length >= 3 ? parts.slice(2).join(":") : session.key;

  if (scope.startsWith("ws-")) {
    const segments = scope.split("-");
    const shortId = segments[segments.length - 1] ?? scope;
    return `Chat ${shortId}`;
  }
  if (scope.startsWith("ws:direct:")) {
    const uuid = scope.replace("ws:direct:", "");
    return `Chat ${uuid.slice(0, 8)}`;
  }
  if (scope.startsWith("team:")) return `Team ${scope.replace("team:", "").slice(0, 12)}`;
  if (scope.startsWith("cron:")) return `Cron ${scope.replace("cron:", "")}`;

  return scope.length > 24 ? scope.slice(0, 21) + "…" : scope;
}

export const SessionSwitcher = memo(function SessionSwitcher({ sessions, activeKey, onSelect, onDelete, onRename, onPin, loading }: SessionSwitcherProps) {
  const { t } = useTranslation("chat");
  const { t: tc } = useTranslation("common");
  const [deleteTarget, setDeleteTarget] = useState<SessionInfo | null>(null);
  const [renameTarget, setRenameTarget] = useState<SessionInfo | null>(null);
  const [title, setTitle] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [pinning, setPinning] = useState(false);
  // Stable partition preserves the caller's recency ordering within each group.
  const orderedSessions = [
    ...sessions.filter((session) => session.metadata?.pinned === "true"),
    ...sessions.filter((session) => session.metadata?.pinned !== "true"),
  ];

  const confirmRename = async () => {
    if (!renameTarget || !title.trim() || !onRename || renaming) return;
    setRenaming(true);
    try {
      if (await onRename(renameTarget.key, title.trim())) setRenameTarget(null);
    } finally {
      setRenaming(false);
    }
  };

  const confirmDelete = () => {
    if (!deleteTarget) return;
    onDelete?.(deleteTarget.key);
    setDeleteTarget(null);
  };

  if (sessions.length === 0 && loading) {
    return (
      <div className="space-y-2 p-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-12 animate-pulse rounded-lg bg-muted" />
        ))}
      </div>
    );
  }

  if (sessions.length === 0) {
    return (
      <div className="px-4 py-8 text-center text-sm text-muted-foreground">
        {tc("noSessions")}
      </div>
    );
  }

  return (
    <>
      <div className="space-y-0.5 p-1.5">
        {orderedSessions.map((session) => {
          const isActive = session.key === activeKey;
          const label = sessionLabel(session);
          const pinned = session.metadata?.pinned === "true";

          return (
            <div
              key={session.key}
              className={`group flex w-full items-center gap-1 rounded-lg pr-1 text-sm transition-colors ${
                isActive ? "bg-accent text-accent-foreground" : "hover:bg-muted"
              }`}
            >
              <button
                type="button"
                onClick={() => onSelect(session.key)}
                aria-current={isActive ? "true" : undefined}
                className="flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-3 py-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <MessageSquare className="h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium text-[13px]">{label}</div>
                  <div className="flex items-center gap-1.5 text-xs-plus text-muted-foreground">
                    <span>{session.messageCount} {tc("messages")}</span>
                    <span>·</span>
                    <span>{formatRelativeTime(session.updated)}</span>
                  </div>
                </div>
              </button>
              {onPin && (
                <button
                  type="button"
                  aria-label={t(pinned ? "unpinChat" : "pinChat")}
                  aria-pressed={pinned}
                  title={t(pinned ? "unpinChat" : "pinChat")}
                  disabled={pinning}
                  onClick={async () => {
                    setPinning(true);
                    try {
                      await onPin(session.key, !pinned);
                    } finally {
                      setPinning(false);
                    }
                  }}
                  className={`flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none transition-opacity hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 [@media(pointer:coarse)]:size-11 ${
                    pinned ? "text-foreground" : "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 max-sm:opacity-100 [@media(pointer:coarse)]:opacity-100"
                  }`}
                >
                  <Pin className={`h-3.5 w-3.5 ${pinned ? "fill-current" : ""}`} />
                </button>
              )}
              {(onRename || onDelete) && (
                <DropdownMenu.Root>
                  <DropdownMenu.Trigger asChild>
                    <button
                      type="button"
                      aria-label={t("chatActions")}
                      title={t("chatActions")}
                      className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 outline-none transition-opacity hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100 max-sm:opacity-100 [@media(pointer:coarse)]:size-11 [@media(pointer:coarse)]:opacity-100"
                    >
                      <MoreHorizontal className="h-4 w-4" />
                    </button>
                  </DropdownMenu.Trigger>
                  <DropdownMenu.Portal>
                    <DropdownMenu.Content
                      align="end"
                      sideOffset={4}
                      onCloseAutoFocus={(event) => {
                        if (renameTarget || deleteTarget) event.preventDefault();
                      }}
                      className="z-50 min-w-36 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
                    >
                      {onRename && (
                        <DropdownMenu.Item
                          onSelect={() => {
                            setTitle(label);
                            setRenameTarget(session);
                          }}
                          className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-2 text-sm outline-none focus:bg-accent focus:text-accent-foreground [@media(pointer:coarse)]:min-h-11"
                        >
                          <Pencil className="h-4 w-4" />
                          {t("renameChat")}
                        </DropdownMenu.Item>
                      )}
                      {onDelete && (
                        <DropdownMenu.Item
                          onSelect={() => setDeleteTarget(session)}
                          className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-2 text-sm text-destructive outline-none focus:bg-destructive/10 [@media(pointer:coarse)]:min-h-11"
                        >
                          <Trash2 className="h-4 w-4" />
                          {tc("delete")}
                        </DropdownMenu.Item>
                      )}
                    </DropdownMenu.Content>
                  </DropdownMenu.Portal>
                </DropdownMenu.Root>
              )}
            </div>
          );
        })}
      </div>
      <Dialog open={!!renameTarget} onOpenChange={(open) => !open && !renaming && setRenameTarget(null)}>
        <DialogContent aria-describedby={undefined}>
          <form onSubmit={(event) => {
            event.preventDefault();
            void confirmRename();
          }}>
            <DialogHeader>
              <DialogTitle>{t("renameChat")}</DialogTitle>
            </DialogHeader>
            <label className="my-4 block space-y-2 text-sm">
              <Input
                autoFocus
                aria-label={t("renameChat")}
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                disabled={renaming}
                className="text-base md:text-sm"
              />
            </label>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={renaming} onClick={() => setRenameTarget(null)}>{tc("cancel")}</Button>
              <Button type="submit" disabled={!title.trim() || renaming}>{tc("save")}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              confirmDelete();
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>{t("deleteChat")}</DialogTitle>
            <DialogDescription>{t("deleteChatConfirm")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>{tc("cancel")}</Button>
            <Button variant="destructive" onClick={confirmDelete}>
              {tc("delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
});
