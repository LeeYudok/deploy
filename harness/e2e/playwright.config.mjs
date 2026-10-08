// 하네스 웹 UI E2E. 모의 LLM 과 하네스를 띄우고 브라우저로 화면을 검사한다.
//
//   E2E_CHANNEL   설치된 브라우저 (msedge / chrome). Windows 기본값은 msedge, 그 밖은 Playwright Chromium
//   E2E_HEADED=1  브라우저 창을 띄워서 본다
//   HARNESS_BIN   이미 빌드한 llmtest(.exe) 를 쓴다 (없으면 go build)
import { defineConfig } from "@playwright/test";

const mockPort = Number(process.env.MOCK_PORT || 18901);
const harnessPort = Number(process.env.HARNESS_PORT || 18902);
const channel = process.env.E2E_CHANNEL || (process.platform === "win32" ? "msedge" : undefined);

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  workers: 1, // 하네스 서버 하나의 설정·기록을 함께 쓰므로 차례로 돌린다
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: `http://127.0.0.1:${harnessPort}`,
    channel,
    headless: !process.env.E2E_HEADED,
    // 해상도를 고정하지 않고 창 크기를 그대로 쓴다. 창을 띄우면 최대화하고,
    // 헤드리스는 창 크기가 800x600 이라 데스크톱 화면이 안 나오므로 1920x1080 창으로 띄운다.
    viewport: null,
    launchOptions: { args: process.env.E2E_HEADED ? ["--start-maximized"] : ["--window-size=1920,1080"] },
    permissions: ["clipboard-read", "clipboard-write"],
    acceptDownloads: true,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop", testIgnore: /mobile/ },
    { name: "mobile", testMatch: /mobile/, use: { viewport: { width: 390, height: 844 } } },
  ],
  webServer: [
    {
      command: "node mock-llm.mjs",
      url: `http://127.0.0.1:${mockPort}/v1/models`,
      env: { MOCK_PORT: String(mockPort) },
      reuseExistingServer: false,
    },
    {
      command: "node start-harness.mjs",
      url: `http://127.0.0.1:${harnessPort}/`,
      env: { MOCK_PORT: String(mockPort), HARNESS_PORT: String(harnessPort) },
      timeout: 180_000,
      reuseExistingServer: false,
    },
  ],
});
