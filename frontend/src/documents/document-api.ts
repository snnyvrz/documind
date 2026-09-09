import { apiFetch } from "@/lib/api";
import type { DocumentDetails, ListedDocument } from "@/documents/document-types";

export async function listDocuments(signal?: AbortSignal) {
  const response = await apiFetch("/documents", { signal });
  if (!response.ok) throw new Error("Could not load documents.");
  return (await response.json()) as ListedDocument[];
}

export async function deleteDocument(documentId: string) {
  const response = await apiFetch(`/documents/${documentId}`, { method: "DELETE" });
  if (!response.ok) throw new Error("Could not delete document.");
}

export async function getDocument(documentId: string, signal: AbortSignal) {
  const response = await apiFetch(`/documents/${documentId}`, { signal });
  const body = (await response.json().catch(() => null)) as DocumentDetails | { error?: string } | null;
  if (!response.ok) throw new Error(body && "error" in body ? body.error : "Could not load document.");
  return body as DocumentDetails;
}
