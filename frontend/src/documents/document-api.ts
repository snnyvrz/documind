import { apiFetch } from "@/lib/api";
import type { DocumentDetails, DocumentListResponse, QuestionHistoryItem } from "@/documents/document-types";

export async function listDocuments(options: { search?: string; cursor?: string; signal?: AbortSignal } = {}) {
  const params = new URLSearchParams();
  if (options.search) params.set("search", options.search);
  if (options.cursor) params.set("cursor", options.cursor);
  const query = params.toString();
  const response = await apiFetch(`/documents${query ? `?${query}` : ""}`, { signal: options.signal });
  if (!response.ok) throw new Error("Could not load documents.");
  const body = await response.json();
  if (Array.isArray(body)) return { items: body, hasMore: false } as DocumentListResponse;
  return body as DocumentListResponse;
}

export async function deleteDocument(documentId: string) {
  const response = await apiFetch(`/documents/${documentId}`, { method: "DELETE" });
  if (!response.ok) throw new Error("Could not delete document.");
}

export async function retryDocument(documentId: string) {
  const response = await apiFetch(`/documents/${documentId}/retry`, { method: "POST" });
  if (!response.ok) throw new Error("Could not retry document processing.");
}

export async function listQuestionHistory(documentId: string, signal: AbortSignal) {
  const response = await apiFetch(`/documents/${documentId}/questions`, { signal });
  if (!response.ok) throw new Error("Could not load question history.");
  return (await response.json()) as QuestionHistoryItem[];
}

export async function getDocument(documentId: string, signal: AbortSignal) {
  const response = await apiFetch(`/documents/${documentId}`, { signal });
  const body = (await response.json().catch(() => null)) as DocumentDetails | { error?: string } | null;
  if (!response.ok) throw new Error(body && "error" in body ? body.error : "Could not load document.");
  return body as DocumentDetails;
}
