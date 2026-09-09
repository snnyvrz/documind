import { DeleteDocumentDialog } from "@/components/delete-document-dialog";
import { DocumentList, type ListedDocument } from "@/components/document-list";
import { DocumentQuestionPanel } from "@/components/document-question-panel";
import { DocumentUploader } from "@/components/document-uploader";
import { useDocumentProcessing } from "@/hooks/use-document-processing";
import { apiFetch } from "@/lib/api";
import { useEffect, useState } from "react";

export const DocumentUploadForm = () => {
  const [documents, setDocuments] = useState<ListedDocument[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [message, setMessage] = useState<{
    type: "error" | "success";
    text: string;
  } | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const { details, error: processingError, detailsById } = useDocumentProcessing(documents, selectedId);

  useEffect(() => {
    void apiFetch("/documents")
      .then(async (response) => {
        if (!response.ok) throw new Error("Could not load documents.");
        setDocuments((await response.json()) as ListedDocument[]);
      })
      .catch((error) =>
        setMessage({
          type: "error",
          text:
            error instanceof Error
              ? error.message
              : "Could not load documents.",
        }),
      );
  }, []);

  useEffect(() => {
    if (!details || details.documentId !== selectedId || (details.status !== "completed" && details.status !== "failed"))
      return;
    // Persist terminal status in the list after the polling request completes.
    Promise.resolve().then(() => {
      setDocuments((current) =>
        current.map((document) =>
          document.documentId === selectedId
            ? {
                ...document,
                status: details.status,
                pageCount: details.pageCount,
                error: details.error,
              }
            : document,
        ),
      );
    });
  }, [details, selectedId]);

  useEffect(() => {
    if (!Object.keys(detailsById).length) return;
    // Polling is an external subscription; mirror terminal results into the list.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDocuments((current) => current.map((document) => {
      const details = detailsById[document.documentId];
      if (!details || (details.status !== "completed" && details.status !== "failed")) return document;
      return { ...document, status: details.status, pageCount: details.pageCount, error: details.error };
    }));
  }, [detailsById]);

  const selectDocument = (nextId: string) => {
    setSelectedId(nextId);
    setMessage(null);
  };

  const handleUploaded = (documentId: string, filename: string) => {
    const document: ListedDocument = {
      documentId,
      filename,
      status: "queued",
      createdAt: new Date().toISOString(),
    };
    setDocuments((current) => [document, ...current]);
    setSelectedId(documentId);
    setMessage(null);
  };

  const deleteDocument = async () => {
    if (!pendingDeleteId) return;
    const id = pendingDeleteId;
    setDeletingId(id);
    try {
      const response = await apiFetch(`/documents/${id}`, { method: "DELETE" });
      if (!response.ok) throw new Error("Could not delete document.");
      setDocuments((current) =>
        current.filter((document) => document.documentId !== id),
      );
      if (selectedId === id) setSelectedId(null);
      setPendingDeleteId(null);
    } catch (error) {
      setMessage({
        type: "error",
        text:
          error instanceof Error ? error.message : "Could not delete document.",
      });
    } finally {
      setDeletingId(null);
    }
  };

  const pendingDocument = documents.find(
    (document) => document.documentId === pendingDeleteId,
  );

  return (
    <div className="space-y-10">
      <DocumentList
        documents={documents}
        selectedId={selectedId}
        onSelect={selectDocument}
        onDelete={setPendingDeleteId}
        deletingId={deletingId}
      />
      <DocumentUploader
        onUploaded={handleUploaded}
        onError={(text) => setMessage({ type: "error", text })}
        onSuccess={(text) => setMessage({ type: "success", text })}
      />
      {message && (
        <p
          className={`text-sm ${message.type === "error" ? "text-destructive" : "text-green-600 dark:text-green-400"}`}
        >
          {message.text}
        </p>
      )}
      {processingError && (
        <p className="text-sm text-destructive">{processingError}</p>
      )}
      {selectedId && (!details || details.documentId !== selectedId || (details.status !== "completed" && details.status !== "failed")) && (
        <p className="rounded-3xl border bg-card/40 p-5 text-sm text-muted-foreground">Loading document details...</p>
      )}
      {selectedId && details?.documentId === selectedId && details.status === "completed" && (
        <DocumentQuestionPanel
          documentId={selectedId}
          filename={details.filename}
          pageCount={details.pageCount}
        />
      )}
      {pendingDocument && (
        <DeleteDocumentDialog
          filename={pendingDocument.filename}
          isDeleting={deletingId === pendingDocument.documentId}
          onCancel={() => setPendingDeleteId(null)}
          onConfirm={() => void deleteDocument()}
        />
      )}
    </div>
  );
};
