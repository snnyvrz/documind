import { Button } from "@/components/ui/button";
import { apiFetch } from "@/lib/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { FileText, Upload, X } from "lucide-react";
import { useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

const uploadSchema = z.object({
  document: z
    .instanceof(File, { error: "Choose a PDF document to continue." })
    .refine((file) => file.type === "application/pdf", "Only PDF documents are supported."),
});

type UploadFormValues = z.infer<typeof uploadSchema>;

type Props = {
  onUploaded: (documentId: string, filename: string) => void;
  onError: (message: string) => void;
  onSuccess: (message: string) => void;
};

export function DocumentUploader({ onUploaded, onError, onSuccess }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File>();
  const [isDragging, setIsDragging] = useState(false);
  const {
    formState: { errors, isSubmitting },
    handleSubmit,
    resetField,
    setError,
    setValue,
  } = useForm<UploadFormValues>({ resolver: zodResolver(uploadSchema) });

  const selectFile = (nextFile: File | undefined) => {
    if (!nextFile) return;
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
    const formData = new FormData();
    formData.append("file", values.document);
    try {
      const response = await apiFetch("/documents", { method: "POST", body: formData });
      const body = (await response.json().catch(() => null)) as {
        documentId?: string;
        error?: string;
      } | null;
      if (!response.ok || !body?.documentId) {
        throw new Error(body?.error ?? "The document could not be uploaded.");
      }
      onUploaded(body.documentId, values.document.name);
      onSuccess("Document uploaded. Preparing it for questions...");
      removeFile();
    } catch (error) {
      onError(error instanceof Error ? error.message : "The document could not be uploaded.");
    }
  };

  return (
    // react-hook-form accesses its internal ref while creating the submit handler.
    // eslint-disable-next-line react-hooks/refs
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
        className={`group flex min-h-56 cursor-pointer flex-col items-center justify-center rounded-3xl border border-dashed px-6 text-center transition-colors ${isDragging ? "border-primary bg-accent" : "border-border bg-card/40 hover:border-primary/60 hover:bg-accent/50"}`}
      >
        <div className="mb-4 flex size-14 items-center justify-center rounded-2xl bg-secondary text-secondary-foreground transition-transform group-hover:scale-105">
          <Upload className="size-6" aria-hidden="true" />
        </div>
        <p className="text-lg font-medium">Drop your PDF here</p>
        <p className="mt-1 text-sm text-muted-foreground">or choose a file from your computer</p>
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
      {errors.document && <p className="text-sm text-destructive">{errors.document.message}</p>}
      {file && (
        <div className="flex items-center gap-3 rounded-2xl border bg-card p-4">
          <FileText className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">{file.name}</p>
            <p className="text-xs text-muted-foreground">{(file.size / 1024 / 1024).toFixed(2)} MB</p>
          </div>
          <Button type="button" variant="ghost" size="icon" aria-label="Remove selected document" onClick={removeFile}>
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
  );
}
