import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:5173";
const usesExternalServer = Boolean(process.env.E2E_BASE_URL);

export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: "list",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    ...devices["Desktop Chrome"],
  },
  timeout: usesExternalServer ? 10 * 60 * 1000 : 30 * 1000,
  webServer: usesExternalServer
    ? undefined
    : {
        command: "bun run dev --host 127.0.0.1",
        url: baseURL,
        reuseExistingServer: !process.env.CI,
      },
});
