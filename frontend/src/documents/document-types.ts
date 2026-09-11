export type DocumentStatus = "queued" | "processing" | "completed" | "failed";
export type FailureKind = "permanent" | "retryable";

export type ListedDocument = {
  documentId: string;
  filename: string;
  status: DocumentStatus;
  pageCount?: number;
  createdAt: string;
  error?: string;
  failureKind?: FailureKind;
};

export type DocumentListResponse = {
  items: ListedDocument[];
  nextCursor?: string;
  hasMore: boolean;
};

export type DocumentDetails = {
  documentId: string;
  filename: string;
  status: DocumentStatus;
  pageCount?: number;
  text?: string;
  error?: string;
  failureKind?: FailureKind;
  attemptCount?: number;
  nextAttemptAt?: string;
};

export type DocumentStatusDetails = Omit<DocumentDetails, "text">;

export type QuestionHistoryItem = {
  id: string;
  documentId: string;
  question: string;
  answer: string;
  sources: import("@/hooks/use-answer-stream").AnswerSource[];
  createdAt: string;
};

export type WorkspaceMessage = {
  type: "error" | "success";
  text: string;
};
