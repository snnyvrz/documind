import { DeleteDocumentDialog } from "@/components/delete-document-dialog";
import { DocumentList } from "@/components/document-list";
import { DocumentQuestionPanel } from "@/components/document-question-panel";
import { DocumentUploader } from "@/components/document-uploader";
import { useDocumentWorkspace } from "@/hooks/use-document-workspace";

export const DocumentUploadForm = () => {
  const workspace = useDocumentWorkspace();

  return (
    <div className="space-y-10">
      <DocumentList
        documents={workspace.documents}
        selectedId={workspace.selectedId}
        onSelect={workspace.selectDocument}
        onDelete={workspace.setPendingDeleteId}
        deletingId={workspace.deletingId}
      />
      <DocumentUploader
        onUploaded={workspace.handleUploaded}
        onError={(text) => workspace.setMessage({ type: "error", text })}
        onSuccess={(text) => workspace.setMessage({ type: "success", text })}
      />
      {workspace.message && (
        <p
          className={`text-sm ${workspace.message.type === "error" ? "text-destructive" : "text-green-600 dark:text-green-400"}`}
        >
          {workspace.message.text}
        </p>
      )}
      {workspace.processingError && (
        <p className="text-sm text-destructive">{workspace.processingError}</p>
      )}
      {workspace.selectedId && (!workspace.details || workspace.details.documentId !== workspace.selectedId || (workspace.details.status !== "completed" && workspace.details.status !== "failed")) && (
        <p className="rounded-3xl border bg-card/40 p-5 text-sm text-muted-foreground">Loading document details...</p>
      )}
      {workspace.selectedId && workspace.details?.documentId === workspace.selectedId && workspace.details.status === "completed" && (
        <DocumentQuestionPanel
          documentId={workspace.selectedId}
          filename={workspace.details.filename}
          pageCount={workspace.details.pageCount}
        />
      )}
      {workspace.pendingDocument && (
        <DeleteDocumentDialog
          filename={workspace.pendingDocument.filename}
          isDeleting={workspace.deletingId === workspace.pendingDocument.documentId}
          onCancel={() => workspace.setPendingDeleteId(null)}
          onConfirm={() => void workspace.handleDelete()}
        />
      )}
    </div>
  );
};
