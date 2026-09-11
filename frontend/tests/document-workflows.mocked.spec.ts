import { expect, test } from "@playwright/test";
import path from "node:path";

const fixture = path.join(import.meta.dirname, "fixtures/sample-document.pdf");

test.describe("mocked API document workflows", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/auth/session", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ subject: "test-user", email: "test@example.com" }) });
    });
  });

  test("loads more documents and searches on the server", async ({ page }) => {
    const requests: string[] = [];
    await page.route(/\/documents(?:\?.*)?$/, async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      const url = new URL(route.request().url());
      requests.push(url.search);
      if (url.searchParams.get("cursor")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ documentId: "older", filename: "older-report.pdf", status: "completed", createdAt: new Date().toISOString() }], hasMore: false }) });
        return;
      }
      if (url.searchParams.get("search")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ documentId: "match", filename: "matching-report.pdf", status: "completed", createdAt: new Date().toISOString() }], hasMore: false }) });
        return;
      }
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ documentId: "newer", filename: "newer-report.pdf", status: "completed", createdAt: new Date().toISOString() }], nextCursor: "older-page", hasMore: true }) });
    });

    await page.goto("/");
    await expect(page.getByText("newer-report.pdf")).toBeVisible();
    await page.getByRole("button", { name: "Load more" }).click();
    await expect(page.getByText("older-report.pdf")).toBeVisible();
    await page.getByLabel("Search documents").fill("matching");
    await expect(page.getByText("matching-report.pdf")).toBeVisible();
    await expect(page.getByText("newer-report.pdf")).not.toBeVisible();
    expect(requests.some((query) => query.includes("search=matching"))).toBe(true);
  });

  test("does not append a delayed pagination response after search changes", async ({ page }) => {
    let paginationStarted!: () => void;
    let releasePagination!: () => void;
    const paginationRequestStarted = new Promise<void>((resolve) => { paginationStarted = resolve; });
    const delayedPagination = new Promise<void>((resolve) => { releasePagination = resolve; });

    await page.route(/\/documents(?:\?.*)?$/, async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      const url = new URL(route.request().url());
      if (url.searchParams.get("cursor")) {
        paginationStarted();
        await delayedPagination;
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ documentId: "stale", filename: "stale-report.pdf", status: "completed", createdAt: new Date().toISOString() }], hasMore: false }) });
        return;
      }
      if (url.searchParams.get("search") === "matching") {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ documentId: "match", filename: "matching-report.pdf", status: "completed", createdAt: new Date().toISOString() }], hasMore: false }) });
        return;
      }
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ documentId: "newer", filename: "newer-report.pdf", status: "completed", createdAt: new Date().toISOString() }], nextCursor: "older-page", hasMore: true }) });
    });

    await page.goto("/");
    await expect(page.getByText("newer-report.pdf")).toBeVisible();
    await page.getByRole("button", { name: "Load more" }).click();
    await paginationRequestStarted;
    await page.getByLabel("Search documents").fill("matching");
    await expect(page.getByText("matching-report.pdf")).toBeVisible();
    releasePagination();
    await expect(page.getByText("stale-report.pdf")).not.toBeVisible();
    await expect(page.getByText("matching-report.pdf")).toBeVisible();
  });

  test("continues polling available documents when another document is missing", async ({ page }) => {
    const documents = [
      { documentId: "missing-document", filename: "missing-report.pdf", status: "processing", createdAt: new Date().toISOString() },
      { documentId: "available-document", filename: "available-report.pdf", status: "processing", createdAt: new Date().toISOString() },
    ];

    await page.route("**/documents", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: documents, hasMore: false }) });
    });
    await page.route("**/documents/missing-document", async (route) => {
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "Document not found." }) });
    });
    await page.route("**/documents/available-document", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ ...documents[1], status: "completed", pageCount: 2 }),
      });
    });

    await page.goto("/");
    await expect(page.getByRole("button", { name: /available-report\.pdf 2 pages/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /missing-report\.pdf processing/ })).toBeVisible();
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
             pageCount: 2,
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
          "event: sources\ndata: {\"sources\":[{\"chunkIndex\":0,\"text\":\"Document processing and retrieval.\",\"pageStart\":1,\"pageEnd\":1}]}\n\n",
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
    await expect(page.getByText("Retrieved passages")).toBeVisible();
    await expect(page.getByText("Document processing and retrieval.")).not.toBeVisible();
    await page.getByText("Retrieved passage 1").click();
    await expect(page.getByText("Document processing and retrieval.")).toBeVisible();
    await page.getByRole("button", { name: "Open in PDF" }).click();
    await expect(page.getByRole("dialog", { name: "Preview sample-document.pdf" })).toBeVisible();
    await expect(page.getByRole("spinbutton", { name: "PDF page number" })).toHaveValue("1");
    const iframeBefore = await page.locator("iframe").elementHandle();
    await page.getByRole("button", { name: "Next PDF page" }).click();
    await expect(page.getByRole("spinbutton", { name: "PDF page number" })).toHaveValue("2");
    const iframeAfter = await page.locator("iframe").elementHandle();
    expect(await iframeBefore?.evaluate((node, current) => node === current, iframeAfter)).toBe(true);
    await expect(page.locator("iframe")).toHaveAttribute("src", "/documents/mock-document-id/file#page=2");
  });

  test("resets selected history and preview when switching completed documents", async ({ page }) => {
    const documents = [
      { documentId: "first-document", filename: "first-report.pdf", status: "completed", pageCount: 2, createdAt: new Date().toISOString() },
      { documentId: "second-document", filename: "second-report.pdf", status: "completed", pageCount: 3, createdAt: new Date().toISOString() },
    ];
    const history = {
      "first-document": [{ id: "first-history", documentId: "first-document", question: "What is the first report about?", answer: "The first report is about document processing.", sources: [{ chunkIndex: 0, text: "First report source", pageStart: 2, pageEnd: 2 }], createdAt: new Date().toISOString() }],
      "second-document": [{ id: "second-history", documentId: "second-document", question: "What is the second report about?", answer: "The second report is about retrieval.", sources: [{ chunkIndex: 0, text: "Second report source", pageStart: 1, pageEnd: 1 }], createdAt: new Date().toISOString() }],
    };

    await page.route("**/documents", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: documents, hasMore: false }) });
    });
    await page.route(/\/documents\/(first-document|second-document)$/, async (route) => {
      const document = documents.find((item) => route.request().url().endsWith(item.documentId));
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(document) });
    });
    await page.route(/\/documents\/(first-document|second-document)\/questions$/, async (route) => {
      const documentId = route.request().url().includes("first-document") ? "first-document" : "second-document";
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(history[documentId]) });
    });

    await page.goto("/");
    await expect(page.getByText("first-report.pdf")).toBeVisible();
    await page.getByRole("button", { name: /first-report\.pdf 2 pages/ }).click();
    await expect(page.getByText("Question history")).toBeVisible();
    await page.getByRole("button", { name: /What is the first report about\?/ }).click();
    await expect(page.locator("p.whitespace-pre-wrap").filter({ hasText: "The first report is about document processing." })).toBeVisible();
    await page.getByText("Retrieved passage 1").click();
    await page.getByRole("button", { name: "Open in PDF" }).click();
    await expect(page.getByRole("dialog", { name: "Preview first-report.pdf" })).toBeVisible();
    await page.getByRole("button", { name: "Close PDF preview" }).click();

    await page.getByRole("button", { name: /second-report\.pdf 3 pages/ }).click();
    await expect(page.locator("p.whitespace-pre-wrap").filter({ hasText: "The second report is about retrieval." })).not.toBeVisible();
    await expect(page.locator("p.whitespace-pre-wrap").filter({ hasText: "The first report is about document processing." })).not.toBeVisible();
    await expect(page.getByRole("dialog", { name: "Preview first-report.pdf" })).not.toBeVisible();
    await expect(page.getByRole("button", { name: /What is the second report about\?/ })).toBeVisible();
  });

  test("restarts polling when retrying the selected failed document", async ({ page }) => {
    let retryRequested = false;
    let detailRequests = 0;
    const document = {
      documentId: "failed-document",
      filename: "failed-report.pdf",
      status: "failed",
      failureKind: "retryable",
      error: "Temporary processor failure",
      createdAt: new Date().toISOString(),
    };

    await page.route("**/documents", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [document], hasMore: false }) });
    });
    await page.route("**/documents/failed-document/retry", async (route) => {
      retryRequested = true;
      await route.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ status: "queued" }) });
    });
    await page.route("**/documents/failed-document", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      detailRequests += 1;
      const body = !retryRequested
        ? document
        : detailRequests === 2
          ? { ...document, status: "queued", error: undefined, failureKind: undefined }
          : { ...document, status: "completed", error: undefined, failureKind: undefined, pageCount: 2 };
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    });
    await page.route("**/documents/failed-document/questions", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
    });

    await page.goto("/");
    await page.getByRole("button", { name: /failed-report\.pdf failed/ }).click();
    await expect(page.getByText("Processing failed")).toBeVisible();
    await page.getByRole("button", { name: "Retry processing" }).click();

    await expect(page.getByRole("heading", { name: "Ask about this document" })).toBeVisible();
    expect(detailRequests).toBeGreaterThanOrEqual(3);
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
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).not.toBeVisible();
    await expect(page.getByRole("button", { name: "Delete old-report.pdf" })).toBeFocused();
    await page.getByRole("button", { name: "Delete old-report.pdf" }).click();
    await page.getByRole("button", { name: "Delete document" }).click();
    await expect(page.getByText("old-report.pdf")).not.toBeVisible();
  });
});
