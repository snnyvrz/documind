import { useEffect, useRef, useState } from "react";
import { apiFetch } from "@/lib/api";

export type AnswerSource = {
  chunkIndex: number;
  text: string;
  pageStart?: number;
  pageEnd?: number;
};

export function useAnswerStream(documentId: string | null) {
  const abortRef = useRef<AbortController | null>(null);
  const [answer, setAnswer] = useState("");
  const [sources, setSources] = useState<AnswerSource[]>([]);
  const [isAsking, setIsAsking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const clear = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    setAnswer("");
    setSources([]);
    setError(null);
    setIsAsking(false);
  };

  useEffect(() => {
    // A document switch must synchronously invalidate the previous answer.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    clear();
    return () => abortRef.current?.abort();
  }, [documentId]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const ask = async (question: string) => {
    if (!documentId || !question.trim() || abortRef.current) return;
    const controller = new AbortController();
    abortRef.current = controller;
    setAnswer("");
    setSources([]);
    setError(null);
    setIsAsking(true);

    try {
      const response = await apiFetch(`/documents/${documentId}/questions`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({ question: question.trim() }),
        signal: controller.signal,
      });
      if (!response.ok || !response.body) {
        const body = (await response.json().catch(() => null)) as { error?: string } | null;
        throw new Error(body?.error ?? "Could not answer the question.");
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let receivedDone = false;
      let successful = false;
      let streamError: string | null = null;
      const processEvent = (value: string) => {
        const data = value.split("\n").find((line) => line.startsWith("data: "))?.slice(6);
        const type = value.split("\n").find((line) => line.startsWith("event: "))?.slice(7);
        if (!data) return;
        const payload = JSON.parse(data) as { ok?: boolean; text?: string; message?: string; sources?: AnswerSource[] };
        if (type === "token") setAnswer((current) => current + (payload.text ?? ""));
        if (type === "sources") setSources(payload.sources ?? []);
        if (type === "error") streamError = payload.message ?? "Could not complete the answer.";
        if (type === "done") {
          receivedDone = true;
          successful = payload.ok === true;
        }
      };

      while (true) {
        const next = await reader.read();
        if (next.done) {
          buffer += decoder.decode();
          if (buffer.trim()) processEvent(buffer);
          break;
        }
        buffer += decoder.decode(next.value, { stream: true });
        const events = buffer.split("\n\n");
        buffer = events.pop() ?? "";
        for (const event of events) processEvent(event);
      }
      if (!receivedDone) throw new Error("Answer generation ended unexpectedly.");
      if (streamError) throw new Error(streamError);
      if (!successful) throw new Error("Could not complete the answer.");
    } catch (cause) {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : "Could not answer the question.");
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        setIsAsking(false);
      }
    }
  };

  const stop = () => {
    if (!abortRef.current) return;
    abortRef.current.abort();
    abortRef.current = null;
    setIsAsking(false);
    setError("Answer generation was stopped before completion.");
  };

  return { answer, sources, isAsking, error, ask, stop, clear };
}
