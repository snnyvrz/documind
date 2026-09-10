import { Button } from "@/components/ui/button";
import { ChevronLeft, ChevronRight, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

type Props = {
  documentId: string;
  filename: string;
  pageCount?: number;
  initialPage: number;
  onClose: () => void;
};

export function PdfPreview({ documentId, filename, pageCount = 1, initialPage, onClose }: Props) {
  const lastPage = Math.max(1, pageCount);
  const [page, setPage] = useState(() => Math.min(Math.max(initialPage, 1), lastPage));
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    closeButtonRef.current?.focus();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), iframe, [href], [tabindex]:not([tabindex="-1"])',
      ));
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, [onClose]);

  const setClampedPage = (value: number) => setPage(Math.min(Math.max(value, 1), lastPage));

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-3 sm:p-6" role="dialog" aria-modal="true" aria-labelledby="pdf-preview-label pdf-preview-title">
      <div ref={dialogRef} className="flex h-[min(92vh,900px)] w-full max-w-5xl flex-col overflow-hidden rounded-3xl border bg-card shadow-2xl">
        <div className="flex items-center justify-between gap-3 border-b px-4 py-3 sm:px-5">
          <div className="min-w-0">
            <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">PDF preview</p>
            <h3 id="pdf-preview-title" className="truncate text-sm font-semibold"><span id="pdf-preview-label" className="sr-only">Preview</span>{filename}</h3>
          </div>
          <Button ref={closeButtonRef} type="button" variant="ghost" size="icon" aria-label="Close PDF preview" onClick={onClose}>
            <X className="size-4" aria-hidden="true" />
          </Button>
        </div>
        <div className="min-h-0 flex-1 bg-muted/40 p-2 sm:p-4">
          <iframe
             title={`${filename}, page ${page}`}
            src={`/documents/${documentId}/file#page=${page}`}
            className="size-full rounded-xl border bg-white"
          />
        </div>
        <div className="flex items-center justify-center gap-3 border-t px-4 py-3">
          <Button type="button" variant="outline" size="icon" aria-label="Previous PDF page" disabled={page <= 1} onClick={() => setClampedPage(page - 1)}>
            <ChevronLeft className="size-4" aria-hidden="true" />
          </Button>
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <span>Page</span>
            <input
              aria-label="PDF page number"
              type="number"
              min={1}
              max={lastPage}
              value={page}
              onChange={(event) => setClampedPage(Number(event.target.value) || 1)}
              className="w-16 rounded-lg border bg-background px-2 py-1.5 text-center text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/30"
            />
            <span>of {lastPage}</span>
          </label>
          <Button type="button" variant="outline" size="icon" aria-label="Next PDF page" disabled={page >= lastPage} onClick={() => setClampedPage(page + 1)}>
            <ChevronRight className="size-4" aria-hidden="true" />
          </Button>
        </div>
      </div>
    </div>
  );
}
