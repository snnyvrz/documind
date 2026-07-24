import { Button } from "@/components/ui/button";
import { zodResolver } from "@hookform/resolvers/zod";
import { FileText, Upload, X } from "lucide-react";
import { useRef, useState } from "react";
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
  const [document, setDocument] = useState<File>();
  const [isDragging, setIsDragging] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploadSuccess, setUploadSuccess] = useState<string | null>(null);
  const {
    formState: { errors, isSubmitting },
    handleSubmit,
    resetField,
    setError,
    setValue,
  } = useForm<UploadFormValues>({
    resolver: zodResolver(uploadSchema),
  });

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
      setUploadSuccess("Document uploaded and ready for analysis.");
    } catch (error) {
      setUploadError(
        error instanceof Error
          ? error.message
          : "The document could not be uploaded.",
      );
    }
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
