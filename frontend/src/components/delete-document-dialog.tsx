import { Button } from "@/components/ui/button";
import { X } from "lucide-react";

type Props = {
  filename: string;
  isDeleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
};

export function DeleteDocumentDialog({ filename, isDeleting, onCancel, onConfirm }: Props) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" role="presentation">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="delete-document-title"
        className="w-full max-w-md rounded-3xl border bg-background p-6 shadow-2xl"
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 id="delete-document-title" className="text-lg font-semibold">Delete document?</h2>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">
              This will permanently remove <span className="font-medium text-foreground">{filename}</span> and its generated data.
            </p>
          </div>
          <Button type="button" variant="ghost" size="icon" aria-label="Close delete dialog" onClick={onCancel} disabled={isDeleting}>
            <X className="size-4" aria-hidden="true" />
          </Button>
        </div>
        <div className="mt-6 flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onCancel} disabled={isDeleting}>Cancel</Button>
          <Button type="button" variant="destructive" onClick={onConfirm} disabled={isDeleting}>
            {isDeleting ? "Deleting..." : "Delete document"}
          </Button>
        </div>
      </div>
    </div>
  );
}
