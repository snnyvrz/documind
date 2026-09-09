import { useEffect, useState } from "react";
import { deleteDocument, listDocuments, listQuestionHistory, retryDocument } from "@/documents/document-api";
import type { ListedDocument, QuestionHistoryItem, WorkspaceMessage } from "@/documents/document-types";
import { useDocumentProcessing } from "@/hooks/use-document-processing";

export function useDocumentWorkspace() {
  const [documents, setDocuments] = useState<ListedDocument[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [message, setMessage] = useState<WorkspaceMessage | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [history, setHistory] = useState<QuestionHistoryItem[]>([]);
  const [retryingId, setRetryingId] = useState<string | null>(null);
  const { details, error: processingError, detailsById } = useDocumentProcessing(documents, selectedId);

  useEffect(() => {
    const controller = new AbortController();
    void listDocuments(controller.signal)
      .then((response) => setDocuments(response.items))
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

  useEffect(() => {
    if (!selectedId) return;
    const controller = new AbortController();
    void listQuestionHistory(selectedId, controller.signal).then(setHistory).catch(() => {
      if (!controller.signal.aborted) setHistory([]);
    });
    return () => controller.abort();
  }, [selectedId]);

  const selectDocument = (documentId: string) => {
    setSelectedId(documentId);
    setHistory([]);
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

  const handleRetry = async (documentId: string) => {
    setRetryingId(documentId);
    try {
      await retryDocument(documentId);
      setDocuments((current) => current.map((document) => document.documentId === documentId ? { ...document, status: "queued", error: undefined } : document));
      setMessage({ type: "success", text: "Processing restarted." });
    } catch (error) {
      setMessage({ type: "error", text: error instanceof Error ? error.message : "Could not retry document processing." });
    } finally { setRetryingId(null); }
  };

  return {
    documents,
    filteredDocuments: documents.filter((document) => document.filename.toLowerCase().includes(search.toLowerCase().trim())),
    search,
    setSearch,
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
    history,
    setHistory,
    retryingId,
    handleRetry,
  };
}
