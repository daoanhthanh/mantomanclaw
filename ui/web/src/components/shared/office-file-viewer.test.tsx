import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OfficeFileViewer } from "./office-file-viewer";
import type * as ReactI18next from "react-i18next";

const runtime = vi.hoisted(() => ({ openSheet: vi.fn(), initDocuments: vi.fn(), openDocument: vi.fn() }));
vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof ReactI18next>()),
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@relybytes/rely-office-react/styles.css", () => ({}));
vi.mock("@relybytes/rely-office-react/fonts.css", () => ({}));
vi.mock("@relybytes/rely-office-react", () => ({
  ...runtime,
  SheetGrid: () => React.createElement("p", null, "Workbook preview"),
  DocumentEditor: () => React.createElement("p", null, "Document preview"),
}));

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
const blob = { arrayBuffer: async () => new ArrayBuffer(1) } as Blob;

async function render(path: string, fetchBlob: (path: string) => Promise<Blob>) {
  await act(async () => { root.render(<OfficeFileViewer key={path} path={path} fetchBlob={fetchBlob} />); });
}

describe("Office preview lifecycle", () => {
  it("discards and frees a workbook that finishes opening after selecting another file", async () => {
    let finish!: (sheet: { free: () => void }) => void;
    runtime.openSheet.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    runtime.initDocuments.mockResolvedValue(undefined);
    runtime.openDocument.mockReturnValue({ document: {} });
    const fetchBlob = vi.fn().mockResolvedValue(blob);
    await render("old.xlsx", fetchBlob);
    await render("current.docx", fetchBlob);
    expect(host.textContent).toContain("Document preview");
    const free = vi.fn();
    await act(async () => { finish({ free }); });
    expect(host.textContent).toContain("Document preview");
    expect(host.textContent).not.toContain("Workbook preview");
    expect(free).toHaveBeenCalledTimes(1);
  });

  it("ignores an old download failure and releases the current workbook on unmount", async () => {
    let fail!: (error: Error) => void;
    const pending = new Promise<Blob>((_resolve, reject) => { fail = reject; });
    const free = vi.fn();
    runtime.openSheet.mockResolvedValue({ free });
    const fetchBlob = vi.fn((path: string) => path === "old.docx" ? pending : Promise.resolve(blob));
    await render("old.docx", fetchBlob);
    await render("current.xlsx", fetchBlob);
    await act(async () => { fail(new Error("old request failed")); });
    expect(host.textContent).toContain("Workbook preview");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    await act(async () => root.render(null));
    expect(free).toHaveBeenCalledTimes(1);
  });
});
