import { useEffect, useRef, useState } from "react";
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
  const [nextCursor, setNextCursor] = useState<string | undefined>();
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [history, setHistory] = useState<QuestionHistoryItem[]>([]);
  const [retryingId, setRetryingId] = useState<string | null>(null);
  const [processingRefreshVersion, setProcessingRefreshVersion] = useState(0);
  const paginationControllerRef = useRef<AbortController | null>(null);
  const resultSetVersionRef = useRef(0);
  const { details, error: processingError, detailsById, invalidateDocument } = useDocumentProcessing(documents, selectedId, processingRefreshVersion);

  useEffect(() => {
    const controller = new AbortController();
    void listDocuments({ search: search.trim(), signal: controller.signal })
      .then((response) => {
        setDocuments(response.items);
        setNextCursor(response.nextCursor);
        setHasMore(response.hasMore);
      })
      .catch((error) => {
        if (!controller.signal.aborted) {
          setMessage({
            type: "error",
            text: error instanceof Error ? error.message : "Could not load documents.",
          });
        }
      });
    return () => controller.abort();
  }, [search]);

  const loadMoreDocuments = async () => {
    if (!hasMore || !nextCursor || loadingMore) return;
    const searchQuery = search.trim();
    const cursor = nextCursor;
    const resultSetVersion = resultSetVersionRef.current;
    const controller = new AbortController();
    paginationControllerRef.current = controller;
    setLoadingMore(true);
    try {
      const response = await listDocuments({ search: searchQuery, cursor, signal: controller.signal });
      if (resultSetVersion !== resultSetVersionRef.current || controller.signal.aborted) return;
      setDocuments((current) => [...current, ...response.items]);
      setNextCursor(response.nextCursor);
      setHasMore(response.hasMore);
    } catch (error) {
      if (!controller.signal.aborted && resultSetVersion === resultSetVersionRef.current) {
        setMessage({ type: "error", text: error instanceof Error ? error.message : "Could not load more documents." });
      }
    } finally {
      if (paginationControllerRef.current === controller) {
        paginationControllerRef.current = null;
        setLoadingMore(false);
      }
    }
  };

  const updateSearch = (value: string) => {
    // A new query starts a new result set; do not append pages from the prior query.
    paginationControllerRef.current?.abort();
    paginationControllerRef.current = null;
    resultSetVersionRef.current += 1;
    setDocuments([]);
    setNextCursor(undefined);
    setHasMore(false);
    setLoadingMore(false);
    setSearch(value);
  };

  useEffect(() => () => paginationControllerRef.current?.abort(), []);

  useEffect(() => {
    if (!Object.keys(detailsById).length) return;
    // Polling is an external subscription; mirror terminal results into the list.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDocuments((current) => current.map((document) => {
      const next = detailsById[document.documentId];
      if (!next || (next.status !== "completed" && next.status !== "failed")) return document;
      return { ...document, status: next.status, pageCount: next.pageCount, error: next.error, failureKind: next.failureKind };
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
      setDocuments((current) => current.map((document) => document.documentId === documentId ? { ...document, status: "queued", error: undefined, failureKind: undefined } : document));
      invalidateDocument(documentId);
      setProcessingRefreshVersion((current) => current + 1);
      setMessage({ type: "success", text: "Processing restarted." });
    } catch (error) {
      setMessage({ type: "error", text: error instanceof Error ? error.message : "Could not retry document processing." });
    } finally { setRetryingId(null); }
  };

  return {
    documents,
    search,
    setSearch: updateSearch,
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
    hasMore,
    loadingMore,
    loadMoreDocuments,
  };
}
