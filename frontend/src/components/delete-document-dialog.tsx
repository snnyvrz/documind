import { Button } from "@/components/ui/button";
import { Dialog } from "@base-ui/react/dialog";
import { X } from "lucide-react";

type Props = {
  filename: string;
  isDeleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
};

export function DeleteDocumentDialog({ filename, isDeleting, onCancel, onConfirm }: Props) {
  return (
    <Dialog.Root open onOpenChange={(open) => !open && !isDeleting && onCancel()}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/60" />
        <Dialog.Viewport className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <Dialog.Popup className="w-full max-w-md rounded-3xl border bg-background p-6 shadow-2xl">
            <div className="flex items-start justify-between gap-4">
              <div>
                <Dialog.Title className="text-lg font-semibold">Delete document?</Dialog.Title>
                <Dialog.Description className="mt-2 text-sm leading-6 text-muted-foreground">
                  This will permanently remove <span className="font-medium text-foreground">{filename}</span> and its generated data.
                </Dialog.Description>
              </div>
              <Dialog.Close render={<Button type="button" variant="ghost" size="icon" aria-label="Close delete dialog" disabled={isDeleting} />}>
                <X className="size-4" aria-hidden="true" />
              </Dialog.Close>
            </div>
            <div className="mt-6 flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={onCancel} disabled={isDeleting}>Cancel</Button>
              <Button type="button" variant="destructive" onClick={onConfirm} disabled={isDeleting}>
                {isDeleting ? "Deleting..." : "Delete document"}
              </Button>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
