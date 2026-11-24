import { useState, useRef, useLayoutEffect } from "react";
import { createPortal } from "react-dom";
import { Split } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useHttp } from "@/hooks/use-ws";
import { useAuthStore } from "@/stores/use-auth-store";
import { usePortalDropdownClose } from "@/hooks/use-portal-dropdown-close";
import { AgentIcon } from "@/components/agents/agent-icon";
import type { AgentData } from "@/types/agent";

interface ForkButtonProps {
  onFork: (agentKey: string) => void;
}

export function ForkButton({ onFork }: ForkButtonProps) {
  const { t } = useTranslation("common");
  const http = useHttp();
  const connected = useAuthStore((s) => s.connected);
  const [open, setOpen] = useState(false);
  const [agents, setAgents] = useState<AgentData[]>([]);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const [dropdownStyle, setDropdownStyle] = useState<React.CSSProperties>({});

  const handleOpen = () => {
    if (!open && agents.length === 0 && connected) {
      http
        .get<{ agents: AgentData[] }>("/v1/agents")
        .then((res) => {
          setAgents((res.agents ?? []).filter((a) => a.status === "active"));
        })
        .catch((err) => console.error("[ForkButton] fetch agents failed:", err));
    }
    setOpen((v) => !v);
  };

  useLayoutEffect(() => {
    if (!open || !triggerRef.current) return;
    const rect = triggerRef.current.getBoundingClientRect();
    const spaceBelow = window.innerHeight - rect.bottom;
    const spaceAbove = rect.top;
    const dropdownHeight = 320; // max-h-80 = 20rem ≈ 320px
    const openAbove = spaceBelow < dropdownHeight && spaceAbove > spaceBelow;
    setDropdownStyle({
      position: "fixed",
      ...(openAbove
        ? { bottom: window.innerHeight - rect.top + 4 }
        : { top: rect.bottom + 4 }),
      left: rect.left,
      minWidth: Math.max(rect.width, 224),
      zIndex: 9999,
    });
  }, [open]);

  usePortalDropdownClose({
    open,
    onClose: () => setOpen(false),
    ignore: [triggerRef, dropdownRef],
  });

  return (
    <div className="relative inline-flex">
      <button
        ref={triggerRef}
        type="button"
        onClick={handleOpen}
        title={t("forkMessage")}
        className="inline-flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
      >
        <Split className="h-4 w-4 rotate-90" />
      </button>

      {open &&
        createPortal(
          <div
            ref={dropdownRef}
            style={dropdownStyle}
            className="pointer-events-auto max-h-60 sm:max-h-80 overflow-y-auto rounded-lg border bg-popover p-1 shadow-md"
          >
            <div className="px-3 py-1.5 text-xs font-medium text-muted-foreground">
              {t("forkTo")}
            </div>
            {agents.length === 0 && (
              <div className="px-3 py-2 text-sm text-muted-foreground">
                {t("noAgentsAvailable")}
              </div>
            )}
            {agents.map((agent) => (
              <button
                key={agent.agent_key}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  onFork(agent.agent_key);
                  setOpen(false);
                }}
                className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm hover:bg-accent"
              >
                <AgentIcon
                  emoji={agent.emoji ?? undefined}
                  size="md"
                  className="shrink-0 text-foreground"
                />
                <span className="flex-1 truncate text-left">
                  {agent.display_name || agent.agent_key}
                </span>
                {agent.is_default && (
                  <span className="text-xs text-muted-foreground">
                    {t("default")}
                  </span>
                )}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}
