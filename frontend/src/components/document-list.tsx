import { Button } from "@/components/ui/button";
import type { ListedDocument } from "@/documents/document-types";
import { FileText, LoaderCircle, Trash2 } from "lucide-react";

type Props = {
  documents: ListedDocument[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onDelete: (id: string) => void;
  deletingId: string | null;
};

export function DocumentList({
  documents,
  selectedId,
  onSelect,
  onDelete,
  deletingId,
}: Props) {
  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground">
          Your documents
        </h2>
        <span className="text-xs text-muted-foreground">
          {documents.length} total
        </span>
      </div>
      {documents.length === 0 ? (
        <p className="border border-dashed p-5 text-sm text-muted-foreground">
          Uploaded documents will appear here.
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
                    {document.error ? `: ${document.error}` : ""}
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
        </div>
      )}
    </section>
  );
}
