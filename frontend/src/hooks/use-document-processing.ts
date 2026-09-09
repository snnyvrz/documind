import { useEffect, useState } from "react";
import { apiFetch } from "@/lib/api";
import type { ListedDocument } from "@/components/document-list";

export type DocumentStatus = "queued" | "processing" | "completed" | "failed";

export type DocumentDetails = {
  documentId: string;
  filename: string;
  status: DocumentStatus;
  pageCount?: number;
  text?: string;
  error?: string;
};

export function useDocumentProcessing(documents: ListedDocument[], documentId: string | null) {
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
          const response = await apiFetch(`/documents/${id}`, { signal: controller.signal });
          const body = (await response.json().catch(() => null)) as DocumentDetails | { error?: string } | null;
          if (!response.ok) throw new Error(body && "error" in body ? body.error : "Could not load document.");
          return body as DocumentDetails;
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
  }, [targetKey]);

  return {
    details: documentId ? detailsById[documentId] ?? null : null,
    error: documentId ? errorsById[documentId] ?? null : null,
    detailsById,
  };
}
