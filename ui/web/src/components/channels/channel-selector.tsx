import { useState, useRef, useLayoutEffect } from "react";
import { createPortal } from "react-dom";
import { ChevronDown } from "lucide-react";
import { usePortalDropdownClose } from "@/hooks/use-portal-dropdown-close";
import { CHANNEL_TYPES } from "@/constants/channels";
import { ChannelIcon } from "./channel-icon";

interface ChannelSelectorProps {
  value: string;
  onChange: (type: string) => void;
  disabled?: boolean;
}

export function ChannelSelector({ value, onChange, disabled }: ChannelSelectorProps) {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const [dropdownStyle, setDropdownStyle] = useState<React.CSSProperties>({});

  useLayoutEffect(() => {
    if (!open || !containerRef.current) return;
    const rect = containerRef.current.getBoundingClientRect();
    setDropdownStyle({
      position: "fixed",
      top: rect.bottom + 4,
      left: rect.left,
      width: rect.width,
      zIndex: 9999,
    });
  }, [open]);

  usePortalDropdownClose({
    open,
    onClose: () => setOpen(false),
    ignore: [containerRef, dropdownRef],
  });

  const selected = CHANNEL_TYPES.find((ct) => ct.value === value);

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => !disabled && setOpen((v) => !v)}
        className="flex w-full items-center gap-2 rounded-lg border bg-background px-3 py-2 text-sm hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50"
      >
        <ChannelIcon type={value} size={16} className="shrink-0" />
        <span className="flex-1 truncate text-left">{selected?.label ?? value}</span>
        <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
      </button>

      {open && createPortal(
        <div
          ref={dropdownRef}
          style={dropdownStyle}
          className="pointer-events-auto overflow-hidden rounded-lg border bg-popover p-1 shadow-md"
        >
          {CHANNEL_TYPES.map((ct) => (
            <button
              key={ct.value}
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => { onChange(ct.value); setOpen(false); }}
              className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm hover:bg-accent ${ct.value === value ? "bg-accent" : ""}`}
            >
              <ChannelIcon type={ct.value} size={16} className="shrink-0" />
              <span className="flex-1 truncate text-left">{ct.label}</span>
            </button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  );
}
