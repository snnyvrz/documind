import { expect, test } from "@playwright/test";
import path from "node:path";

const fixture = path.join(import.meta.dirname, "fixtures/sample-document.pdf");

test.describe("mocked API document workflows", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/auth/session", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ subject: "test-user", email: "test@example.com" }) });
    });
  });
  test("selects and removes a PDF", async ({ page }) => {
    await page.goto("/");

    await page.locator('input[type="file"]').setInputFiles(fixture);
    await expect(page.getByRole("paragraph").filter({ hasText: "sample-document.pdf" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Upload document" })).toBeEnabled();

    await page.getByRole("button", { name: "Remove selected document" }).click();
    await expect(page.getByRole("paragraph").filter({ hasText: "sample-document.pdf" })).not.toBeVisible();
    await expect(page.getByRole("button", { name: "Upload document" })).toBeDisabled();
  });

  test("uploads a PDF and asks a question", async ({ page }) => {
    await page.route("**/documents", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ documentId: "mock-document-id", status: "queued" }),
      });
    });
    await page.route("**/documents/mock-document-id", async (route) => {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          documentId: "mock-document-id",
          filename: "sample-document.pdf",
          status: "completed",
          text: "Extracted text from the mocked document.",
        }),
      });
    });
    await page.route("**/documents/mock-document-id/questions", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: [
          "event: token\ndata: {\"text\":\"The document explains \"}\n\n",
          "event: token\ndata: {\"text\":\"document processing.\"}\n\n",
          "event: sources\ndata: {\"sources\":[{\"chunkIndex\":0,\"text\":\"Document processing and retrieval.\"}]}\n\n",
          "event: done\ndata: {\"ok\":true}\n\n",
        ].join(""),
      });
    });
    await page.route("**/documents/mock-document-id/chunks", async (route) => {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify([{ chunkIndex: 0, text: "Document processing and retrieval.", pageStart: 1, pageEnd: 1 }]),
      });
    });

    await page.goto("/");
    await page.locator('input[type="file"]').setInputFiles(fixture);
    await page.getByRole("button", { name: "Upload document" }).click();

    await expect(page.getByText("Document ready")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Ask about this document" })).toBeVisible();

    await page.getByPlaceholder("What is this document about?").fill("What does it explain?");
    await page.getByRole("button", { name: "Ask" }).click();

    await expect(page.getByText("The document explains document processing.")).toBeVisible();
    await expect(page.getByText("Extracted text from the mocked document.")).not.toBeVisible();
    await expect(page.getByText("Document processing and retrieval.")).not.toBeVisible();
  });

  test("confirms document deletion in a modal", async ({ page }) => {
    await page.route("**/documents", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          contentType: "application/json",
          body: JSON.stringify([{ documentId: "document-to-delete", filename: "old-report.pdf", status: "completed", pageCount: 2, createdAt: new Date().toISOString() }]),
        });
        return;
      }
      await route.continue();
    });
    await page.route("**/documents/document-to-delete", async (route) => {
      if (route.request().method() === "DELETE") {
        await route.fulfill({ status: 204 });
        return;
      }
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ documentId: "document-to-delete", filename: "old-report.pdf", status: "completed", pageCount: 2 }),
      });
    });

    await page.goto("/");
    await expect(page.getByText("old-report.pdf")).toBeVisible();
    await page.getByRole("button", { name: "Delete old-report.pdf" }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(page.getByText("This will permanently remove")).toBeVisible();
    await page.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("dialog")).not.toBeVisible();
    await page.getByRole("button", { name: "Delete old-report.pdf" }).click();
    await page.getByRole("button", { name: "Delete document" }).click();
    await expect(page.getByText("old-report.pdf")).not.toBeVisible();
  });
});
