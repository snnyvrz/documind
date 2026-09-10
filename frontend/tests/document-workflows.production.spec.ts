import { expect, test } from "@playwright/test";
import path from "node:path";

const fixture = path.join(import.meta.dirname, "fixtures/sample-document.pdf");

test("production-like: uploads a PDF and asks a question", async ({ page }) => {
  const email = `e2e-${Date.now()}@example.com`;
  await page.goto("/register");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("e2e-password-123");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByText("Your document workspace")).toBeVisible();
  await page.locator('input[type="file"]').setInputFiles(fixture);
  await page.getByRole("button", { name: "Upload document" }).click();

  await expect(page.getByText("Document ready")).toBeVisible({ timeout: 5 * 60 * 1000 });
  await expect(page.getByRole("heading", { name: "Ask about this document" })).toBeVisible();

  await page.getByPlaceholder("What is this document about?").fill("What does this document explain?");
  await page.getByRole("button", { name: "Ask" }).click();

  await expect(page.getByRole("button", { name: "Ask" })).toBeVisible({ timeout: 5 * 60 * 1000 });
  await expect(page.locator("p.border").filter({ hasText: /./ })).toBeVisible();
  await expect(page.getByText("Retrieved passages")).toBeVisible();
});
