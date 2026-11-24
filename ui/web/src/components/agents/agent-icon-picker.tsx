import { useState, useRef, useLayoutEffect } from "react";
import { createPortal } from "react-dom";
import { ChevronDown } from "lucide-react";
import { AgentIcon, AGENT_ICONS } from "./agent-icon";
import { cn } from "@/lib/utils";
import { usePortalDropdownClose } from "@/hooks/use-portal-dropdown-close";

interface AgentIconPickerProps {
  value?: string;
  onChange: (value: string) => void;
  className?: string;
}

/** Extract the first emoji grapheme cluster from a string, or return empty. */
function extractSingleEmoji(str: string): string {
  const match =
    str.match(/\p{Emoji_Presentation}(\u200D\p{Emoji_Presentation})*/u) ??
    str.match(/\p{Extended_Pictographic}(\uFE0F?\u200D\p{Extended_Pictographic})*/u);
  return match?.[0] ?? "";
}

/**
 * Trigger button + portal dropdown for picking an agent icon.
 *
 * Value format:
 *  - `"icon:<name>"` for SVG icons from /agent-icons/
 *  - Any emoji string for legacy emoji
 *  - Empty/undefined for the Bot fallback
 */
export function AgentIconPicker({ value, onChange, className }: AgentIconPickerProps) {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLButtonElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const [dropdownStyle, setDropdownStyle] = useState<React.CSSProperties>({});

  useLayoutEffect(() => {
    if (!open || !containerRef.current) return;
    const rect = containerRef.current.getBoundingClientRect();
    const dropW = 232;
    // Flip left if too close to right edge
    const left =
      rect.left + dropW > window.innerWidth ? window.innerWidth - dropW - 8 : rect.left;
    setDropdownStyle({
      position: "fixed",
      top: rect.bottom + 4,
      left,
      width: dropW,
      zIndex: 9999,
    });
  }, [open]);

  usePortalDropdownClose({
    open,
    onClose: () => setOpen(false),
    ignore: [containerRef, dropdownRef],
  });

  const emojiValue = value?.startsWith("icon:") ? "" : (value ?? "");

  return (
    <div className={cn("relative inline-block", className)}>
      <button
        ref={containerRef}
        type="button"
        onClick={() => setOpen((p) => !p)}
        className="flex h-9 w-14 items-center justify-center gap-0.5 rounded-md border border-input bg-background text-foreground hover:bg-muted transition-colors shrink-0"
        title="Choose agent icon"
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <AgentIcon emoji={value} size="md" />
        <ChevronDown className="h-3 w-3 text-muted-foreground" />
      </button>

      {open &&
        createPortal(
          <div
            ref={dropdownRef}
            className="pointer-events-auto rounded-lg border border-border bg-popover p-2 shadow-lg"
            style={dropdownStyle}
            role="listbox"
            aria-label="Agent icon"
          >
            {/* SVG icon grid */}
            <div className="grid grid-cols-5 gap-1">
              {AGENT_ICONS.map(({ id, label }) => {
                const iconValue = `icon:${id}`;
                const selected = value === iconValue;
                return (
                  <button
                    key={id}
                    type="button"
                    title={label}
                    role="option"
                    aria-selected={selected}
                    onClick={() => {
                      onChange(iconValue);
                      setOpen(false);
                    }}
                    className={cn(
                      "flex h-9 w-9 items-center justify-center rounded-md text-foreground transition-colors hover:bg-accent",
                      selected && "bg-primary/10 ring-1 ring-primary text-primary",
                    )}
                  >
                    <AgentIcon emoji={iconValue} size="md" />
                  </button>
                );
              })}
            </div>

            {/* Custom emoji input (backward compat) */}
            <div className="mt-2 border-t border-border pt-2">
              <p className="mb-1 text-xs text-muted-foreground">or type emoji</p>
              <input
                type="text"
                placeholder="🤖"
                value={emojiValue}
                onChange={(e) => {
                  const raw = e.target.value;
                  if (!raw) {
                    onChange("");
                    return;
                  }
                  const single = extractSingleEmoji(raw);
                  if (single) onChange(single);
                }}
                className="w-full rounded-md border border-input bg-background px-2 py-1 text-base md:text-sm focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>,
          document.body,
        )}
    </div>
  );
}
