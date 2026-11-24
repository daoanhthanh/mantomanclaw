import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SessionSwitcher } from "./session-switcher";
import { useChatSessions } from "@/pages/chat/hooks/use-chat-sessions";
import type { SessionInfo } from "@/types/session";

const mocks = vi.hoisted(() => ({ call: vi.fn(), error: vi.fn(), select: vi.fn(), delete: vi.fn() }));
vi.mock("@/hooks/use-ws", () => ({ useWs: () => ws }));
vi.mock("@/hooks/use-ws-event", () => ({ useWsEvent: () => {} }));
vi.mock("@/stores/use-auth-store", () => ({ useAuthStore: (selector: (state: { connected: boolean }) => unknown) => selector({ connected: true }) }));
vi.mock("@/stores/use-toast-store", () => ({ toast: { error: mocks.error, success: vi.fn() } }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/lib/error-utils", () => ({ userFriendlyError: (error: Error) => error.message }));
const ws = { call: mocks.call };
let root: Root;
let container: HTMLDivElement;
let stored: SessionInfo[];

function Harness() {
  const { sessions, renameSession, pinSession } = useChatSessions("agent");
  return <SessionSwitcher sessions={sessions} activeKey="new" onSelect={mocks.select} onDelete={mocks.delete} onRename={renameSession} onPin={pinSession} />;
}

function button(label: string, scope: ParentNode = document) {
  const result = Array.from(scope.querySelectorAll<HTMLButtonElement>("button")).find((node) => node.getAttribute("aria-label") === label || node.textContent === label);
  if (!result) throw new Error(`Missing button: ${label}`);
  return result;
}
async function click(node: Element) {
  await act(async () => { node.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
}
async function menuItem(text: string) {
  await act(async () => {
    button("chatActions").dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0, ctrlKey: false }));
  });
  const item = Array.from(document.querySelectorAll('[role="menuitem"]')).find((node) => node.textContent === text);
  if (!item) throw new Error(`Missing menu item: ${text}`);
  await click(item);
}
async function fillTitle(value: string) {
  const input = document.querySelector("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function mount() {
  await act(async () => { root.render(<Harness />); });
}

beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.clearAllMocks();
  stored = [
    { key: "new", label: "Recent", messageCount: 2, created: "2026-01-01", updated: "2026-02-02", metadata: { display_name: "Recent display", untouched: "keep" } },
    { key: "old", label: "Older", messageCount: 1, created: "2026-01-01", updated: "2026-02-01" },
  ];
  mocks.call.mockImplementation(async (method: string, params: { key: string; label?: string; metadata?: Record<string, string> }) => {
    if (method === "sessions.list") return { sessions: structuredClone(stored) };
    if (method === "sessions.patch") {
      stored = stored.map((session) => session.key === params.key ? { ...session, ...(params.label ? { label: params.label } : {}), metadata: { ...session.metadata, ...params.metadata } } : session);
      return { ok: true, key: params.key };
    }
    throw new Error(`Unexpected method: ${method}`);
  });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  await mount();
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

describe("conversation actions", () => {
  it("pins an older conversation above recent ones, persists on remount, and unpins without selecting it", async () => {
    const olderPin = container.querySelectorAll('[aria-label="pinChat"]')[1]!;
    await click(olderPin);
    expect(container.querySelector('[aria-current]')?.parentElement?.previousElementSibling?.textContent).toContain("Older");
    expect(mocks.select).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    expect(container.querySelector('[aria-label="unpinChat"]')).not.toBeNull();
    expect(container.firstElementChild?.firstElementChild?.textContent).toContain("Older");
    await click(button("unpinChat"));
    expect(container.firstElementChild?.firstElementChild?.textContent).toContain("Recent display");
    expect(stored[1]?.metadata?.pinned).toBe("false");
  });

  it("renames the displayed title, trims whitespace and preserves unrelated metadata after remount", async () => {
    await menuItem("renameChat");
    expect(document.querySelector("input")?.value).toBe("Recent display");
    await fillTitle("  My conversation  ");
    await click(button("save"));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(stored[0]).toMatchObject({ label: "My conversation", metadata: { chat_title: "My conversation", untouched: "keep" } });
    await act(async () => root.unmount());
    root = createRoot(container);
    await mount();
    expect(container.textContent).toContain("My conversation");
    expect(mocks.select).not.toHaveBeenCalled();
  });

  it("rejects blank names and retains the dialog and old title if saving fails", async () => {
    await menuItem("renameChat");
    await fillTitle("   ");
    expect(button("save").disabled).toBe(true);
    await fillTitle("New title");
    mocks.call.mockRejectedValueOnce(new Error("Permission denied"));
    await click(button("save"));
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    expect(container.textContent).toContain("Recent display");
    expect(mocks.error).toHaveBeenCalled();
    await click(button("cancel"));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("does not show a successful pin when persistence fails", async () => {
    mocks.call.mockRejectedValueOnce(new Error("Disconnected"));
    await click(button("pinChat"));
    expect(container.querySelector('[aria-label="unpinChat"]')).toBeNull();
    expect(mocks.error).toHaveBeenCalled();
  });

  it("keeps deletion behind confirmation, supports cancel and the existing Enter behavior", async () => {
    await menuItem("delete");
    expect(mocks.delete).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("deleteChatConfirm");
    await click(button("cancel"));
    expect(mocks.delete).not.toHaveBeenCalled();
    await menuItem("delete");
    await act(async () => {
      document.querySelector('[role="dialog"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(mocks.delete).toHaveBeenCalledExactlyOnceWith("new");
    expect(mocks.select).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });
});
