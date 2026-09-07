import { useEffect, useState } from "react";

export type DocumentStatus = "queued" | "processing" | "completed" | "failed";

export type DocumentDetails = {
  documentId: string;
  filename: string;
  status: DocumentStatus;
  pageCount?: number;
  text?: string;
  error?: string;
};

export function useDocumentProcessing(documentId: string | null) {
  const [details, setDetails] = useState<DocumentDetails | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!documentId) {
      // Reset state when the selected document is cleared.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDetails(null);
      setError(null);
      return;
    }

    const controller = new AbortController();
    let timeout: number | undefined;
    let active = true;

    const poll = async () => {
      try {
        const response = await fetch(`/documents/${documentId}`, { signal: controller.signal });
        const body = (await response.json().catch(() => null)) as DocumentDetails | { error?: string } | null;
        if (!response.ok) throw new Error(body && "error" in body ? body.error : "Could not load document.");
        if (!active) return;
        const next = body as DocumentDetails;
        setDetails(next);
        setError(null);
        if (next.status !== "completed" && next.status !== "failed") {
          timeout = window.setTimeout(() => void poll(), 1000);
        }
      } catch (cause) {
        if (!active || controller.signal.aborted) return;
        setError(cause instanceof Error ? cause.message : "Could not check document status.");
        timeout = window.setTimeout(() => void poll(), 2000);
      }
    };

    void poll();
    return () => {
      active = false;
      controller.abort();
      if (timeout !== undefined) window.clearTimeout(timeout);
    };
  }, [documentId]);

  return { details, error };
}
