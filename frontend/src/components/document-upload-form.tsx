import { DocumentList, type ListedDocument } from "@/components/document-list";
import { Button } from "@/components/ui/button";
import { useAnswerStream, type AnswerSource } from "@/hooks/use-answer-stream";
import { useDocumentProcessing } from "@/hooks/use-document-processing";
import { zodResolver } from "@hookform/resolvers/zod";
import { FileText, Upload, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { apiFetch } from "@/lib/api";

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
  const [file, setFile] = useState<File>();
  const [documents, setDocuments] = useState<ListedDocument[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploadSuccess, setUploadSuccess] = useState<string | null>(null);
  const [question, setQuestion] = useState("");
  const [chunks, setChunks] = useState<AnswerSource[]>([]);
  const [highlightedChunk, setHighlightedChunk] = useState<number | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [isDragging, setIsDragging] = useState(false);
  const { details, error: processingError } = useDocumentProcessing(selectedId);
  const answerStream = useAnswerStream(selectedId);
  const {
    formState: { errors, isSubmitting },
    handleSubmit,
    resetField,
    setError,
    setValue,
  } = useForm<UploadFormValues>({ resolver: zodResolver(uploadSchema) });

  const loadDocuments = async () => {
    const response = await apiFetch("/documents");
    if (!response.ok) throw new Error("Could not load documents.");
    setDocuments((await response.json()) as ListedDocument[]);
  };

  useEffect(() => {
    void Promise.resolve()
      .then(loadDocuments)
      .catch((error) =>
        setUploadError(
          error instanceof Error ? error.message : "Could not load documents.",
        ),
      );
  }, []);

  useEffect(() => {
    if (
      !selectedId ||
      !details ||
      (details.status !== "completed" && details.status !== "failed")
    )
      return;
    Promise.resolve().then(() =>
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
      ),
    );
  }, [details, selectedId]);

  useEffect(() => {
    if (!selectedId || details?.status !== "completed") {
      Promise.resolve().then(() => setChunks([]));
      return;
    }
    const controller = new AbortController();
    void apiFetch(`/documents/${selectedId}/chunks`, {
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) throw new Error("Could not load document chunks.");
        setChunks((await response.json()) as AnswerSource[]);
      })
      .catch((error) => {
        if (!controller.signal.aborted)
          setUploadError(
            error instanceof Error
              ? error.message
              : "Could not load document chunks.",
          );
      });
    return () => controller.abort();
  }, [details?.status, selectedId]);

  const selectDocument = (nextId: string) => {
    setSelectedId(nextId);
    setQuestion("");
    setUploadError(null);
    setUploadSuccess(null);
    setHighlightedChunk(null);
  };

  const selectFile = (nextFile: File | undefined) => {
    if (!nextFile) return;
    setUploadError(null);
    if (nextFile.type !== "application/pdf") {
      setError("document", { message: "Only PDF documents are supported." });
      return;
    }
    setValue("document", nextFile, { shouldValidate: true });
    setFile(nextFile);
  };

  const removeFile = () => {
    resetField("document");
    setFile(undefined);
    if (inputRef.current) inputRef.current.value = "";
  };

  const onSubmit = async (values: UploadFormValues) => {
    setUploadError(null);
    setUploadSuccess(null);
    const formData = new FormData();
    formData.append("file", values.document);
    try {
      const response = await apiFetch("/documents", {
        method: "POST",
        body: formData,
      });
      const body = (await response.json().catch(() => null)) as {
        documentId?: string;
        error?: string;
      } | null;
      if (!response.ok || !body?.documentId)
        throw new Error(body?.error ?? "The document could not be uploaded.");
      const document: ListedDocument = {
        documentId: body.documentId,
        filename: values.document.name,
        status: "queued",
        createdAt: new Date().toISOString(),
      };
      setDocuments((current) => [document, ...current]);
      selectDocument(document.documentId);
      setUploadSuccess("Document uploaded. Extracting text...");
      removeFile();
    } catch (error) {
      setUploadError(
        error instanceof Error
          ? error.message
          : "The document could not be uploaded.",
      );
    }
  };

  const deleteDocument = async (id: string) => {
    const document = documents.find((item) => item.documentId === id);
    if (!document || !window.confirm(`Delete ${document.filename}?`)) return;
    setDeletingId(id);
    try {
      const response = await apiFetch(`/documents/${id}`, { method: "DELETE" });
      if (!response.ok) throw new Error("Could not delete document.");
      setDocuments((current) =>
        current.filter((item) => item.documentId !== id),
      );
      if (selectedId === id) {
        setSelectedId(null);
        setChunks([]);
        setQuestion("");
      }
    } catch (error) {
      setUploadError(
        error instanceof Error ? error.message : "Could not delete document.",
      );
    } finally {
      setDeletingId(null);
    }
  };

  const goToCitation = (source: AnswerSource) => {
    const target = document.getElementById(`chunk-${source.chunkIndex}`);
    if (!target) return;
    target.scrollIntoView({ behavior: "smooth", block: "center" });
    setHighlightedChunk(source.chunkIndex);
    window.setTimeout(
      () =>
        setHighlightedChunk((current) =>
          current === source.chunkIndex ? null : current,
        ),
      1800,
    );
  };

  return (
    <div className="space-y-10">
      <DocumentList
        documents={documents}
        selectedId={selectedId}
        onSelect={selectDocument}
        onDelete={(id) => void deleteDocument(id)}
        deletingId={deletingId}
      />
      {/* react-hook-form accesses its internal ref while creating the submit handler. */}
      {/* eslint-disable-next-line react-hooks/refs */}
      <form onSubmit={handleSubmit(onSubmit)} className="space-y-5">
        <input
          ref={inputRef}
          type="file"
          accept="application/pdf,.pdf"
          className="sr-only"
          onChange={(event) => selectFile(event.target.files?.[0])}
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
            selectFile(event.dataTransfer.files[0]);
          }}
          className={`flex min-h-56 cursor-pointer flex-col items-center justify-center border border-dashed px-6 text-center transition-colors ${isDragging ? "border-primary bg-accent" : "border-border hover:border-primary/60 hover:bg-accent/50"}`}
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
        {uploadError && (
          <p className="text-sm text-destructive">{uploadError}</p>
        )}
        {processingError && (
          <p className="text-sm text-destructive">{processingError}</p>
        )}
        {uploadSuccess && (
          <p className="text-sm text-green-600 dark:text-green-400">
            {uploadSuccess}
          </p>
        )}
        {details?.status === "completed" && (
          <p className="text-sm text-green-600 dark:text-green-400">
            Document processed successfully.
          </p>
        )}
        {file && (
          <div className="flex items-center gap-3 border p-4">
            <FileText
              className="size-5 shrink-0 text-muted-foreground"
              aria-hidden="true"
            />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{file.name}</p>
              <p className="text-xs text-muted-foreground">
                {(file.size / 1024 / 1024).toFixed(2)} MB
              </p>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Remove selected document"
              onClick={removeFile}
            >
              <X className="size-4" aria-hidden="true" />
            </Button>
          </div>
        )}
        <div className="flex justify-end">
          <Button type="submit" disabled={!file}>
            {isSubmitting ? "Uploading..." : "Upload document"}
          </Button>
        </div>
      </form>
      {selectedId && details?.status === "completed" && (
        <section className="space-y-5 border-t pt-5">
          <div>
            <h3 className="text-lg font-medium">Ask about this document</h3>
            <p className="text-sm text-muted-foreground">
              {details.filename} · {details.pageCount ?? 0} pages
            </p>
          </div>
          <div className="flex gap-2">
            <input
              value={question}
              onChange={(event) => setQuestion(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void answerStream.ask(question);
                }
              }}
              placeholder="What is this document about?"
              className="min-w-0 flex-1 border bg-background px-3 py-2 text-sm"
            />
            <Button
              type="button"
              onClick={() =>
                answerStream.isAsking
                  ? answerStream.stop()
                  : void answerStream.ask(question)
              }
              disabled={!answerStream.isAsking && !question.trim()}
            >
              {answerStream.isAsking ? "Stop" : "Ask"}
            </Button>
          </div>
          {answerStream.error && (
            <p className="text-sm text-destructive">{answerStream.error}</p>
          )}
          {answerStream.answer && (
            <p className="whitespace-pre-wrap border p-4 text-sm">
              {answerStream.answer}
            </p>
          )}
          {answerStream.sources.length > 0 && (
            <div className="space-y-2">
              <p className="text-sm font-medium">Sources</p>
              {answerStream.sources.map((source) => (
                <button
                  type="button"
                  key={source.chunkIndex}
                  onClick={() => goToCitation(source)}
                  className="block w-full border-l-2 pl-3 text-left text-sm text-muted-foreground hover:border-primary hover:text-foreground"
                >
                  <span className="font-medium text-foreground">
                    Chunk {source.chunkIndex}: {source.text}
                  </span>
                  <span className="ml-2 text-xs">
                    (pages {source.pageStart ?? "?"}
                    {source.pageEnd && source.pageEnd !== source.pageStart
                      ? `-${source.pageEnd}`
                      : ""}
                    )
                  </span>
                </button>
              ))}
            </div>
          )}
          <div className="space-y-3 border-t pt-5">
            <h4 className="text-sm font-medium">Extracted text</h4>
            <pre className="max-h-96 overflow-auto whitespace-pre-wrap border p-4 text-sm">
              {details.text || "No embedded text found in this PDF."}
            </pre>
            {chunks.length > 0 && (
              <div className="space-y-2">
                <h5 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                  Source chunks
                </h5>
                {chunks.map((chunk) => (
                  <p
                    id={`chunk-${chunk.chunkIndex}`}
                    key={chunk.chunkIndex}
                    className={`scroll-mt-8 whitespace-pre-wrap border p-3 text-sm transition-colors ${highlightedChunk === chunk.chunkIndex ? "bg-accent ring-2 ring-primary" : ""}`}
                  >
                    {chunk.text}
                  </p>
                ))}
              </div>
            )}
          </div>
        </section>
      )}
    </div>
  );
};
