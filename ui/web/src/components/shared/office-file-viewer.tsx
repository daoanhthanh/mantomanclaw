import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { extOf } from "@/lib/file-helpers";
import { ErrorBoundary } from "./error-boundary";

const OFFICE_KIND: Record<string, "document" | "sheet" | "slides"> = {
  doc: "document", docx: "document", docm: "document", odt: "document",
  xls: "sheet", xlsx: "sheet", xlsm: "sheet", ods: "sheet",
  ppt: "slides", pptx: "slides", pptm: "slides", odp: "slides",
};

export function isOfficeFile(path: string): boolean {
  return Object.prototype.hasOwnProperty.call(OFFICE_KIND, extOf(path));
}

export function OfficeFileViewer({ path, fetchBlob, onDownload }: {
  path: string;
  fetchBlob: (path: string) => Promise<Blob>;
  onDownload?: () => void;
}) {
  const { t } = useTranslation("common");
  const [preview, setPreview] = useState<{ path: string; fetchBlob: typeof fetchBlob; content: ReactNode } | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let dispose: (() => void) | undefined;
    setFailed(false);
    setPreview(null);

    async function load() {
      try {
        const [office, blob] = await Promise.all([
          import("@relybytes/rely-office-react"),
          fetchBlob(path),
          import("@relybytes/rely-office-react/styles.css"),
          import("@relybytes/rely-office-react/fonts.css"),
        ]);
        if (cancelled) return;
        const bytes = new Uint8Array(await blob.arrayBuffer());
        const ext = extOf(path);
        let content: ReactNode;
        if (OFFICE_KIND[ext] === "sheet") {
          const sheet = await office.openSheet(bytes, path);
          dispose = () => sheet.free();
          if (cancelled) { dispose(); dispose = undefined; return; }
          content = <office.SheetGrid sheet={sheet} editable={false} poweredBy={false} />;
        } else {
          await office.initDocuments();
          if (cancelled) return;
          if (OFFICE_KIND[ext] === "document") {
            const { document } = office.openDocument(bytes, { fileName: path });
            content = <office.DocumentEditor document={document} editable={false} toolbar={false} poweredBy={false} />;
          } else {
            content = <office.SlideEditor presentation={office.readPresentation(bytes)} editable={false} poweredBy={false} />;
          }
        }
        if (!cancelled) setPreview({ path, fetchBlob, content });
      } catch {
        dispose?.();
        dispose = undefined;
        if (!cancelled) setFailed(true);
      }
    }
    void load();
    return () => { cancelled = true; dispose?.(); };
  }, [path, fetchBlob]);

  const error = (
    <div role="alert" className="flex flex-col items-center gap-3 py-8 text-center text-sm text-muted-foreground">
      <p>{t("officePreviewFailed")}</p>
      {onDownload && <Button variant="outline" size="sm" onClick={onDownload}><Download className="mr-2 h-4 w-4" />{t("download")}</Button>}
    </div>
  );
  if (failed) {
    return error;
  }
  if (!preview || preview.path !== path || preview.fetchBlob !== fetchBlob) {
    return <div role="status" className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground"><Loader2 className="h-5 w-5 animate-spin" />{t("officePreviewLoading")}</div>;
  }
  return (
    <div className={`office-file-preview min-w-0 overflow-x-auto${OFFICE_KIND[extOf(path)] === "sheet" ? " h-full" : ""}`}>
      <ErrorBoundary key={path} fallback={error}>{preview.content}</ErrorBoundary>
    </div>
  );
}
