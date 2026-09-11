import { useEffect, useState } from "react";
import { getDocument } from "@/documents/document-api";
import type { DocumentDetails, ListedDocument } from "@/documents/document-types";

export function useDocumentProcessing(documents: ListedDocument[], documentId: string | null, refreshVersion = 0) {
  const [detailsById, setDetailsById] = useState<Record<string, DocumentDetails>>({});
  const [errorsById, setErrorsById] = useState<Record<string, string>>({});

  const targetIds = documents
    .filter((document) => document.status !== "completed" && document.status !== "failed")
    .map((document) => document.documentId);
  if (documentId && !targetIds.includes(documentId)) targetIds.push(documentId);
  const targetKey = targetIds.join(",");

  useEffect(() => {
    if (!targetKey) {
      return;
    }

    const controller = new AbortController();
    const ids = targetKey.split(",");
    let timeout: number | undefined;
    let active = true;
    let delay = 1000;

    const poll = async (immediate = false) => {
      if (!immediate && document.hidden) return;
      try {
        const responses = await Promise.all(ids.map(async (id) => {
          return getDocument(id, controller.signal);
        }));
        if (!active) return;
        setDetailsById((current) => Object.fromEntries([
          ...Object.entries(current),
          ...responses.map((next) => [next.documentId, next]),
        ]));
        setErrorsById((current) => {
          const next = { ...current };
          for (const detail of responses) delete next[detail.documentId];
          return next;
        });
        delay = 1000;
        if (responses.some((next) => next.status !== "completed" && next.status !== "failed")) {
          timeout = window.setTimeout(() => void poll(), delay);
        }
      } catch (cause) {
        if (!active || controller.signal.aborted) return;
        setErrorsById((current) => ({
          ...current,
          ...Object.fromEntries(ids.map((id) => [id, cause instanceof Error ? cause.message : "Could not check document status."])),
        }));
        delay = Math.min(delay * 2, 8000);
        timeout = window.setTimeout(() => void poll(), delay);
      }
    };

    const handleVisibilityChange = () => {
      if (!document.hidden) void poll(true);
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    void poll(true);
    return () => {
      active = false;
      controller.abort();
      if (timeout !== undefined) window.clearTimeout(timeout);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [targetKey, refreshVersion]);

  const invalidateDocument = (id: string) => {
    setDetailsById((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
    setErrorsById((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
  };

  return {
    details: documentId ? detailsById[documentId] ?? null : null,
    error: documentId ? errorsById[documentId] ?? null : null,
    detailsById,
    invalidateDocument,
  };
}
