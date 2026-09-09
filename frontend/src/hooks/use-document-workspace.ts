import { useEffect, useState } from "react";
import { deleteDocument, listDocuments } from "@/documents/document-api";
import type { ListedDocument, WorkspaceMessage } from "@/documents/document-types";
import { useDocumentProcessing } from "@/hooks/use-document-processing";

export function useDocumentWorkspace() {
  const [documents, setDocuments] = useState<ListedDocument[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [message, setMessage] = useState<WorkspaceMessage | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const { details, error: processingError, detailsById } = useDocumentProcessing(documents, selectedId);

  useEffect(() => {
    const controller = new AbortController();
    void listDocuments(controller.signal)
      .then(setDocuments)
      .catch((error) => {
        if (!controller.signal.aborted) {
          setMessage({
            type: "error",
            text: error instanceof Error ? error.message : "Could not load documents.",
          });
        }
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!Object.keys(detailsById).length) return;
    // Polling is an external subscription; mirror terminal results into the list.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDocuments((current) => current.map((document) => {
      const next = detailsById[document.documentId];
      if (!next || (next.status !== "completed" && next.status !== "failed")) return document;
      return { ...document, status: next.status, pageCount: next.pageCount, error: next.error };
    }));
  }, [detailsById]);

  const selectDocument = (documentId: string) => {
    setSelectedId(documentId);
    setMessage(null);
  };

  const handleUploaded = (documentId: string, filename: string) => {
    setDocuments((current) => [{
      documentId,
      filename,
      status: "queued",
      createdAt: new Date().toISOString(),
    }, ...current]);
    setSelectedId(documentId);
    setMessage(null);
  };

  const handleDelete = async () => {
    if (!pendingDeleteId) return;
    const documentId = pendingDeleteId;
    setDeletingId(documentId);
    try {
      await deleteDocument(documentId);
      setDocuments((current) => current.filter((document) => document.documentId !== documentId));
      if (selectedId === documentId) setSelectedId(null);
      setPendingDeleteId(null);
    } catch (error) {
      setMessage({
        type: "error",
        text: error instanceof Error ? error.message : "Could not delete document.",
      });
    } finally {
      setDeletingId(null);
    }
  };

  return {
    documents,
    selectedId,
    details,
    processingError,
    deletingId,
    pendingDeleteId,
    pendingDocument: documents.find((document) => document.documentId === pendingDeleteId),
    message,
    setMessage,
    selectDocument,
    handleUploaded,
    setPendingDeleteId,
    handleDelete,
  };
}
