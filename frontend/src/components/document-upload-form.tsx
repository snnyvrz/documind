import { DeleteDocumentDialog } from "@/components/delete-document-dialog";
import { DocumentList } from "@/components/document-list";
import { DocumentQuestionPanel } from "@/components/document-question-panel";
import { DocumentUploader } from "@/components/document-uploader";
import { useDocumentWorkspace } from "@/hooks/use-document-workspace";

export const DocumentUploadForm = () => {
  const workspace = useDocumentWorkspace();

  return (
    <div className="grid gap-6 lg:grid-cols-[280px_minmax(0,1fr)]">
      <aside className="min-h-0 rounded-2xl border bg-card/30 p-4 lg:h-[calc(100vh-9rem)]">
      <DocumentList
        documents={workspace.documents}
        selectedId={workspace.selectedId}
        onSelect={workspace.selectDocument}
        onDelete={workspace.setPendingDeleteId}
        deletingId={workspace.deletingId}
        search={workspace.search}
        onSearch={workspace.setSearch}
        onRetry={workspace.handleRetry}
        retryingId={workspace.retryingId}
      />
      <DocumentUploader
        onUploaded={workspace.handleUploaded}
        onError={(text) => workspace.setMessage({ type: "error", text })}
        onSuccess={(text) => workspace.setMessage({ type: "success", text })}
      />
      </aside>
      <section className="min-w-0 space-y-6">
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
        <div className="rounded-3xl border bg-card/40 p-6">
          <p className="text-xs font-semibold uppercase tracking-wider text-primary">{workspace.details?.status === "processing" ? "Processing" : "Queued"}</p>
          <h3 className="mt-2 text-2xl font-semibold">Preparing your document</h3>
          <p className="mt-2 text-sm text-muted-foreground">We are extracting text and creating searchable passages. Questions will be available when this finishes.</p>
          <div className="mt-5 h-2 overflow-hidden rounded-full bg-muted"><div className="h-full w-1/2 animate-pulse rounded-full bg-primary" /></div>
        </div>
      )}
      {workspace.selectedId && workspace.details?.documentId === workspace.selectedId && workspace.details.status === "failed" && (
        <div className="rounded-3xl border border-destructive/30 bg-destructive/5 p-6">
          <p className="text-xs font-semibold uppercase tracking-wider text-destructive">Processing failed</p>
          <h3 className="mt-2 text-2xl font-semibold">This document needs another attempt</h3>
          <p className="mt-2 text-sm text-muted-foreground">{workspace.details.error ?? "The document could not be prepared for questions."}</p>
          <button type="button" className="mt-5 rounded-2xl bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50" disabled={workspace.retryingId === workspace.selectedId} onClick={() => void workspace.handleRetry(workspace.selectedId!)}>{workspace.retryingId === workspace.selectedId ? "Retrying..." : "Retry processing"}</button>
        </div>
      )}
      {workspace.selectedId && workspace.details?.documentId === workspace.selectedId && workspace.details.status === "completed" && (
        <DocumentQuestionPanel
          documentId={workspace.selectedId}
          filename={workspace.details.filename}
          pageCount={workspace.details.pageCount}
          history={workspace.history}
          onHistory={() => undefined}
          onNewHistory={(item) => workspace.setHistory((current) => current.some((entry) => entry.id === item.id) ? current : [item, ...current])}
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
      </section>
    </div>
  );
};
