const { defineConfig } = require("@playwright/test");

module.exports = defineConfig({
  testDir: "./tests",
  timeout: 30000,
  use: {
    baseURL: process.env.API_BASE_URL || "http://127.0.0.1:8080",
    channel: "chromium",
    trace: "retain-on-failure"
  },
  reporter: "line"
});
