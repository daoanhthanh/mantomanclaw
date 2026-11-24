import { useState, useEffect, useRef, useLayoutEffect } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { ChevronDown } from "lucide-react";
import { useHttp } from "@/hooks/use-ws";
import { usePortalDropdownClose } from "@/hooks/use-portal-dropdown-close";
import { useAuthStore } from "@/stores/use-auth-store";
import type { AgentData } from "@/types/agent";
import { AgentIcon } from "@/components/agents/agent-icon";

interface AgentSelectorProps {
  value: string;
  onChange: (agentId: string) => void;
  /** Open the dropdown above the trigger (for selectors anchored at the screen bottom). */
  dropUp?: boolean;
  /** "default": bordered full-width trigger. "ghost": borderless, content-width trigger (inline use). */
  variant?: "default" | "ghost";
}


export function AgentSelector({ value, onChange, dropUp, variant = "default" }: AgentSelectorProps) {
  const { t } = useTranslation("common");
  const http = useHttp();
  const connected = useAuthStore((s) => s.connected);
  const [agents, setAgents] = useState<AgentData[]>([]);
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const [dropdownStyle, setDropdownStyle] = useState<React.CSSProperties>({});

  useEffect(() => {
    if (!connected) return;
    http
      .get<{ agents: AgentData[] }>("/v1/agents")
      .then((res) => {
        const active = (res.agents ?? []).filter((a) => a.status === "active");
        setAgents(active);
      })
      .catch((err) => console.error("[AgentSelector] fetch agents failed:", err));
  }, [http, connected]);

  useLayoutEffect(() => {
    if (!open || !containerRef.current) return;
    const rect = containerRef.current.getBoundingClientRect();
    setDropdownStyle({
      position: "fixed",
      ...(dropUp
        ? { bottom: window.innerHeight - rect.top + 4 }
        : { top: rect.bottom + 4 }),
      left: rect.left,
      width: variant === "ghost" ? Math.max(rect.width, 224) : rect.width,
      zIndex: 9999,
    });
  }, [open, dropUp, variant]);

  usePortalDropdownClose({
    open,
    onClose: () => setOpen(false),
    ignore: [containerRef, dropdownRef],
  });

  const selected = agents.find((a) => a.agent_key === value);
  const selectedEmoji = selected?.emoji || undefined;

  return (
    <div ref={containerRef} className={variant === "ghost" ? "relative min-w-0" : "relative"}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className={
          variant === "ghost"
            ? "flex max-w-[180px] items-center gap-1.5 rounded-lg px-2 py-1 text-sm text-foreground hover:bg-accent"
            : "flex w-full items-center gap-2 rounded-lg border bg-background px-3 py-2 text-sm hover:bg-accent"
        }
      >
          <AgentIcon emoji={selectedEmoji} size="md" className="shrink-0 text-foreground" />
        <span className="flex-1 truncate text-left font-medium">
          {selected?.display_name ?? selected?.agent_key ?? (value || t("selectAgent"))}
        </span>
        <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
      </button>

      {open && createPortal(
        <div
          ref={dropdownRef}
          style={dropdownStyle}
          className="pointer-events-auto max-h-60 sm:max-h-80 overflow-y-auto rounded-lg border bg-popover p-1 shadow-md"
        >
          {agents.length === 0 && (
            <div className="px-3 py-2 text-sm text-muted-foreground">
              {t("noAgentsAvailable")}
            </div>
          )}
          {agents.map((agent) => {
            const emoji = agent.emoji || undefined;
            return (
              <button
                key={agent.agent_key}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => { onChange(agent.agent_key); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm hover:bg-accent ${
                  agent.agent_key === value ? "bg-accent" : ""
                }`}
              >
                <AgentIcon emoji={emoji} size="md" className="shrink-0 text-foreground" />
                <span className="flex-1 truncate text-left">
                  {agent.display_name || agent.agent_key}
                </span>
                {agent.is_default && (
                  <span className="text-xs text-muted-foreground">{t("default")}</span>
                )}
              </button>
            );
          })}
        </div>,
        document.body,
      )}
    </div>
  );
}
