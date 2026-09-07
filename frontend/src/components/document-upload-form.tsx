import { Button } from "@/components/ui/button";
import { zodResolver } from "@hookform/resolvers/zod";
import { FileText, Upload, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

const uploadSchema = z.object({
  document: z
    .instanceof(File, { error: "Choose a PDF document to continue." })
    .refine(
      (file) => file.type === "application/pdf",
      "Only PDF documents are supported.",
    ),
});

type UploadFormValues = z.infer<typeof uploadSchema>;

export const DocumentUploadForm = () => {
  const inputRef = useRef<HTMLInputElement>(null);
  const answerAbortRef = useRef<AbortController | null>(null);
  const [document, setDocument] = useState<File>();
  const [isDragging, setIsDragging] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploadSuccess, setUploadSuccess] = useState<string | null>(null);
  const [documentId, setDocumentId] = useState<string | null>(null);
  const [readyDocumentId, setReadyDocumentId] = useState<string | null>(null);
  const [extractedText, setExtractedText] = useState<string | null>(null);
  const [question, setQuestion] = useState("");
  const [answer, setAnswer] = useState("");
  const [sources, setSources] = useState<Array<{ chunkIndex: number; text: string }>>([]);
  const [isAsking, setIsAsking] = useState(false);
  const {
    formState: { errors, isSubmitting },
    handleSubmit,
    resetField,
    setError,
    setValue,
  } = useForm<UploadFormValues>({
    resolver: zodResolver(uploadSchema),
  });

  useEffect(() => {
    if (!documentId) return;
    const poll = async () => {
      const response = await fetch(`/documents/${documentId}`);
      if (!response.ok) return;
      const body = (await response.json()) as { status: string; text?: string; error?: string };
      if (body.status === "completed") {
        setExtractedText(body.text ?? "");
        setReadyDocumentId(documentId);
        setUploadSuccess("Document processed successfully.");
        setDocumentId(null);
      } else if (body.status === "failed") {
        setUploadError(body.error ?? "Text extraction failed.");
        setDocumentId(null);
      }
    };
    const interval = window.setInterval(() => void poll(), 1000);
    void poll();
    return () => window.clearInterval(interval);
  }, [documentId]);

  useEffect(() => () => answerAbortRef.current?.abort(), []);

  const selectDocument = (file: File | undefined) => {
    if (!file) return;

    setUploadError(null);
    setUploadSuccess(null);

    if (file.type !== "application/pdf") {
      setError("document", { message: "Only PDF documents are supported." });
      return;
    }

    setValue("document", file, { shouldValidate: true });
    setDocument(file);
  };

  const removeDocument = () => {
    resetField("document");
    setDocument(undefined);
    setUploadError(null);
    setUploadSuccess(null);
    if (inputRef.current) inputRef.current.value = "";
  };

  const onSubmit = async (values: UploadFormValues) => {
    setUploadError(null);
    setUploadSuccess(null);

    const formData = new FormData();
    formData.append("file", values.document);

    try {
      const response = await fetch("/documents", {
        method: "POST",
        body: formData,
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: string;
        } | null;
        throw new Error(body?.error ?? "The document could not be uploaded.");
      }
      const body = (await response.json()) as { documentId: string };
      setDocumentId(body.documentId);
      setReadyDocumentId(null);
      setExtractedText(null);
      setUploadSuccess("Document uploaded. Extracting text...");
    } catch (error) {
      setUploadError(
        error instanceof Error
          ? error.message
          : "The document could not be uploaded.",
      );
    }
  };

  const askQuestion = async () => {
    if (!readyDocumentId || !question.trim()) return;
    answerAbortRef.current?.abort();
    const abortController = new AbortController();
    answerAbortRef.current = abortController;
    setIsAsking(true);
    setAnswer("");
    setSources([]);
    try {
      const response = await fetch(`/documents/${readyDocumentId}/questions`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({ question }),
        signal: abortController.signal,
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
        const payload = JSON.parse(data) as { ok?: boolean; text?: string; message?: string; sources?: Array<{ chunkIndex: number; text: string }> };
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
        for (const value of events) {
          processEvent(value);
        }
      }
      if (!receivedDone) throw new Error("Answer generation ended unexpectedly.");
      if (streamError) throw new Error(streamError);
      if (!successful) throw new Error("Could not complete the answer.");
    } catch (error) {
      if (!abortController.signal.aborted) {
        setUploadError(error instanceof Error ? error.message : "Could not answer the question.");
      }
    } finally {
      if (answerAbortRef.current === abortController) answerAbortRef.current = null;
      setIsAsking(false);
    }
  };

  const stopAnswer = () => {
    setUploadError("Answer generation was stopped before completion.");
    answerAbortRef.current?.abort();
  };

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="space-y-5">
      <input
        ref={inputRef}
        type="file"
        accept="application/pdf,.pdf"
        className="sr-only"
        onChange={(event) => selectDocument(event.target.files?.[0])}
      />

      <div
        role="button"
        tabIndex={0}
        aria-label="Upload a PDF document"
        onClick={() => inputRef.current?.click()}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            inputRef.current?.click();
          }
        }}
        onDragEnter={(event) => {
          event.preventDefault();
          setIsDragging(true);
        }}
        onDragOver={(event) => event.preventDefault()}
        onDragLeave={(event) => {
          event.preventDefault();
          setIsDragging(false);
        }}
        onDrop={(event) => {
          event.preventDefault();
          setIsDragging(false);
          selectDocument(event.dataTransfer.files[0]);
        }}
        className={`flex min-h-72 cursor-pointer flex-col items-center justify-center border border-dashed px-6 text-center transition-colors ${
          isDragging
            ? "border-primary bg-accent"
            : "border-border hover:border-primary/60 hover:bg-accent/50"
        }`}
      >
        <div className="mb-4 flex size-12 items-center justify-center rounded-full bg-secondary text-secondary-foreground">
          <Upload className="size-5" aria-hidden="true" />
        </div>
        <p className="text-lg font-medium">Drop your PDF here</p>
        <p className="mt-1 text-sm text-muted-foreground">
          or choose a file from your computer
        </p>
        <Button
          type="button"
          variant="outline"
          className="mt-5"
          onClick={(event) => {
            event.stopPropagation();
            inputRef.current?.click();
          }}
        >
          Browse files
        </Button>
      </div>

      {errors.document && (
        <p className="text-sm text-destructive">{errors.document.message}</p>
      )}
      {uploadError && <p className="text-sm text-destructive">{uploadError}</p>}
      {uploadSuccess && (
        <p className="text-sm text-green-600 dark:text-green-400">
          {uploadSuccess}
        </p>
      )}
      {extractedText !== null && (
        <pre className="max-h-96 overflow-auto whitespace-pre-wrap border p-4 text-sm">
          {extractedText || "No embedded text found in this PDF."}
        </pre>
      )}

      {readyDocumentId && (
        <section className="space-y-4 border-t pt-5">
          <h3 className="text-lg font-medium">Ask about this document</h3>
          <div className="flex gap-2">
            <input
              value={question}
              onChange={(event) => setQuestion(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void askQuestion();
                }
              }}
              placeholder="What is this document about?"
              className="min-w-0 flex-1 border bg-background px-3 py-2 text-sm"
            />
            <Button type="button" onClick={() => (isAsking ? stopAnswer() : void askQuestion())} disabled={!isAsking && !question.trim()}>{isAsking ? "Stop" : "Ask"}</Button>
          </div>
          {answer && <p className="whitespace-pre-wrap border p-4 text-sm">{answer}</p>}
          {sources.length > 0 && <div className="space-y-2"><p className="text-sm font-medium">Sources</p>{sources.map((source) => <p key={source.chunkIndex} className="border-l-2 pl-3 text-sm text-muted-foreground">Chunk {source.chunkIndex}: {source.text}</p>)}</div>}
        </section>
      )}

      {document && (
        <div className="flex items-center gap-3 border p-4">
          <FileText
            className="size-5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">{document.name}</p>
            <p className="text-xs text-muted-foreground">
              {(document.size / 1024 / 1024).toFixed(2)} MB
            </p>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Remove selected document"
            onClick={removeDocument}
          >
            <X className="size-4" aria-hidden="true" />
          </Button>
        </div>
      )}

      <div className="flex justify-end">
        <Button type="submit" disabled={!document}>
          {isSubmitting ? "Uploading..." : "Upload document"}
        </Button>
      </div>
    </form>
  );
};
