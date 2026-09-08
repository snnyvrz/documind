import { expect, test } from "@playwright/test";

test.describe("mocked authentication workflows", () => {
  test("shows login and registration pages when signed out", async ({ page }) => {
    await page.route("**/auth/session", (route) => route.fulfill({ status: 401, body: JSON.stringify({ error: "not authenticated" }) }));
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Your documents, ready when you are." })).toBeVisible();
    await page.getByRole("link", { name: "Create an account" }).click();
    await expect(page.getByRole("heading", { name: "Make your documents searchable." })).toBeVisible();
  });

  test("renders the account and logs out", async ({ page }) => {
    await page.route("**/auth/session", (route) => route.fulfill({ status: 200, body: JSON.stringify({ subject: "test-user", email: "test@example.com" }) }));
    await page.route("**/documents", (route) => route.fulfill({ status: 200, contentType: "application/json", body: "[]" }));
    let loggedOut = false;
    await page.route("**/auth/logout", async (route) => { loggedOut = true; await route.fulfill({ status: 204 }); });
    await page.goto("/");
    await expect(page.getByText("test@example.com")).toBeVisible();
    await page.getByRole("button", { name: "Log out" }).click();
    await expect.poll(() => loggedOut).toBe(true);
    await expect(page.getByRole("heading", { name: "Your documents, ready when you are." })).toBeVisible();
  });
});
