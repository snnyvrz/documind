import { Button } from "@/components/ui/button";
import type { ListedDocument } from "@/documents/document-types";
import { FileText, LoaderCircle, RotateCcw, Search, Trash2 } from "lucide-react";

type Props = {
  documents: ListedDocument[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onDelete: (id: string) => void;
  deletingId: string | null;
  search: string;
  onSearch: (value: string) => void;
  onRetry: (id: string) => void;
  retryingId: string | null;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
};

export function DocumentList({
  documents,
  selectedId,
  onSelect,
  onDelete,
  deletingId,
  search,
  onSearch,
  onRetry,
  retryingId,
  hasMore,
  loadingMore,
  onLoadMore,
}: Props) {
  return (
    <section className="flex min-h-0 flex-col gap-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground">
          Documents
        </h2>
        <span className="text-xs text-muted-foreground">
          {documents.length} loaded
        </span>
      </div>
      <label className="relative block">
        <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
        <input value={search} onChange={(event) => onSearch(event.target.value)} placeholder="Search documents" aria-label="Search documents" className="h-9 w-full rounded-xl border bg-background pl-9 pr-3 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/30" />
      </label>
      {documents.length === 0 ? (
        <p className="border border-dashed p-5 text-sm text-muted-foreground">
          {search.trim() ? "No documents match your search." : "Uploaded documents will appear here."}
        </p>
      ) : (
        <div className="divide-y border">
          {documents.map((document) => (
            <div
              key={document.documentId}
              className={`flex items-center gap-3 p-3 ${selectedId === document.documentId ? "bg-accent" : ""}`}
            >
              <button
                type="button"
                className="flex min-w-0 flex-1 items-center gap-3 text-left"
                onClick={() => onSelect(document.documentId)}
              >
                <FileText
                  className="size-4 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium">
                    {document.filename}
                  </span>
                  <span className="block text-xs capitalize text-muted-foreground">
                    {document.status === "completed"
                      ? `${document.pageCount ?? 0} pages`
                      : document.status}
                  </span>
                </span>
              </button>
              <span className="flex items-center gap-1">
                {document.status !== "completed" &&
                  document.status !== "failed" && (
                    <LoaderCircle
                      className="size-4 animate-spin text-muted-foreground"
                      aria-label="Processing"
                    />
                  )}
                {document.status === "failed" && <Button type="button" variant="ghost" size="icon" aria-label={`Retry ${document.filename}`} disabled={retryingId === document.documentId} onClick={() => onRetry(document.documentId)}><RotateCcw className="size-4" aria-hidden="true" /></Button>}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={`Delete ${document.filename}`}
                  disabled={deletingId === document.documentId}
                  onClick={() => onDelete(document.documentId)}
                >
                  <Trash2 className="size-4" aria-hidden="true" />
                </Button>
              </span>
            </div>
          ))}
          {hasMore && <div className="p-3"><Button type="button" variant="outline" className="w-full" disabled={loadingMore} onClick={onLoadMore}>{loadingMore ? "Loading..." : "Load more"}</Button></div>}
        </div>
      )}
    </section>
  );
}
