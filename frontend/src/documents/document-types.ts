export type DocumentStatus = "queued" | "processing" | "completed" | "failed";

export type ListedDocument = {
  documentId: string;
  filename: string;
  status: DocumentStatus;
  pageCount?: number;
  createdAt: string;
  error?: string;
};

export type DocumentDetails = {
  documentId: string;
  filename: string;
  status: DocumentStatus;
  pageCount?: number;
  text?: string;
  error?: string;
};

export type WorkspaceMessage = {
  type: "error" | "success";
  text: string;
};
