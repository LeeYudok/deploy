// 하네스 웹 UI E2E (데스크톱). 모의 LLM 의 답은 mock-llm.mjs 참고.
import { expect, test } from "@playwright/test";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const tmp = join(dirname(fileURLToPath(import.meta.url)), "..", ".tmp");
const mock = `http://127.0.0.1:${process.env.MOCK_PORT || 18901}`;

// 하네스 서버 하나의 대화·설정·기록을 이어서 쓰므로 차례로, 같은 페이지에서 돌린다.
test.describe.configure({ mode: "serial" });

/** @type {import('@playwright/test').Page} */
let page;

test.beforeAll(async ({ browser }, testInfo) => {
  // beforeAll 에서는 page 픽스처를 못 쓰므로 설정의 use 옵션을 그대로 넘겨 컨텍스트를 만든다.
  const { baseURL, viewport, permissions, acceptDownloads } = testInfo.project.use;
  const context = await browser.newContext({ baseURL, viewport, permissions, acceptDownloads });
  page = await context.newPage();
  await page.goto("/");
  await page.evaluate(() => localStorage.clear());
  await page.reload();
});

test.afterAll(async () => {
  await page.context().close();
});

async function tab(name) {
  await page.locator(`.tabs button[data-tab=${name}]`).click();
}

// send 는 대화 탭에서 한 턴을 보내고 그 답변 칸을 돌려준다.
async function send(text, think) {
  const answers = page.locator(".msg.assistant");
  const before = await answers.count();
  if (think) await page.locator(`.composer label:has(input[value=${think}])`).click();
  const q = page.locator("textarea[name=q]");
  await q.fill(text);
  await q.press("Enter");
  await expect(answers).toHaveCount(before + 1);
  return answers.nth(before);
}

async function mockRequests() {
  return (await page.request.get(`${mock}/__requests`)).json();
}

test("연결 상태와 모델 목록", async () => {
  await expect(page.locator("#statusServer")).toHaveText("sglang 연결됨");
  await expect(page.locator("#modelList option[value='mock-model']")).toHaveCount(1);
  await expect(page.locator("select[name=preset]")).toHaveValue("mock");
});

test("멀티턴 대화와 thinking on/off", async () => {
  await tab("chat");
  const a1 = await send("내 이름은 민수야", "off");
  await expect(a1.locator(".meta")).toContainText("think off");
  await expect(a1.locator(".answer")).toContainText("민수");
  await expect(a1.locator("details.think")).toBeHidden();

  const a2 = await send("내 이름이 뭐였지?", "on");
  await expect(a2.locator(".meta")).toContainText("think on");
  await expect(a2.locator(".answer")).toContainText("민수 입니다");
  await expect(a2.locator("details.think")).toBeVisible();
  await expect(page.locator("#turnCount")).toHaveText("2턴");

  const reqs = (await mockRequests()).slice(-2);
  expect(reqs[0].body.chat_template_kwargs).toEqual({ thinking: false, enable_thinking: false });
  expect(reqs[1].body.chat_template_kwargs).toEqual({ thinking: true, enable_thinking: true });
  // 두 번째 턴은 앞 대화를 함께 보낸다 (system + user + assistant + user). 추론은 히스토리에 넣지 않는다.
  expect(reqs[1].body.messages.map((m) => m.role)).toEqual(["system", "user", "assistant", "user"]);
  expect(reqs[1].body.messages[2].content).not.toContain("생각:");
  // 키는 저장된 그 주소로만 간다.
  expect(reqs[1].auth).toBe("Bearer e2e-secret-key");
});

test("스트리밍 중지", async () => {
  const turns = await page.locator("#turnCount").textContent();
  const a = await send("천천히 세어 줘", "off");
  await expect(a.locator(".answer")).not.toBeEmpty();
  await page.locator("#stopChat").click();
  await expect(a.locator(".error-box")).toContainText("중지했습니다");
  await expect(page.locator("#turnCount")).toHaveText(turns);
  await expect(page.locator("#send")).toBeVisible();
});

test("본문 없이 추론만 온 답변 안내", async () => {
  const a = await send("빈본문", "on");
  await expect(a.locator(".empty-answer")).toContainText("본문 없이 추론만");
  await expect(a.locator(".chip.warn")).toHaveText("본문 0자");
  await expect(a.locator("details.think")).toHaveAttribute("open", "");
});

test("코드 블록 복사와 다운로드", async () => {
  const a = await send("코드 줘", "off");
  const block = a.locator(".code-block").first();
  await expect(block.locator(".code-head")).toContainText("go");
  await expect(block.locator(".code-head")).toContainText("main.go");

  const [download] = await Promise.all([page.waitForEvent("download"), block.locator(".code-btn").nth(1).click()]);
  expect(download.suggestedFilename()).toBe("main.go");
  const file = await download.path();
  expect(readFileSync(file, "utf8")).toContain("package main");

  await block.locator(".code-btn").first().click();
  await expect(block.locator(".code-btn").first()).toContainText("복사됨");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("func main()");
});

test("새 대화", async () => {
  await page.locator("#resetChat").click();
  await expect(page.locator(".empty h2")).toHaveText("무엇을 테스트할까요?");
  await expect(page.locator("#turnCount")).toHaveText("");
});

test("시나리오 실행과 요약", async () => {
  await tab("scenario");
  await expect(page.locator(".sc-card")).toHaveCount(2);
  await page.locator(".segmented label:has(input[name=scThink][value=''])").click();
  await page.locator("#scRun").click();
  await expect(page.locator("#scRun")).toBeVisible({ timeout: 30_000 });
  const tiles = page.locator("#scSummary .tile");
  await expect(tiles.nth(0)).toContainText("80%"); // 검사 5개 중 4개 통과
  await expect(tiles.nth(1)).toContainText("1 / 2");
  await expect(page.locator(".board-row", { hasText: "e2e-memory" })).toContainText("PASS");
  await expect(page.locator(".board-row", { hasText: "e2e-fail" })).toContainText("FAIL 1");
});

test("프리셋 전환과 연결 실패 표시", async () => {
  const preset = page.locator("select[name=preset]");
  await preset.selectOption("other");
  await expect(page.locator("input[name=base_url]")).toHaveValue("http://127.0.0.1:9/v1");
  await expect(page.locator("input[name=api_key]")).toHaveAttribute("placeholder", "없음");
  await expect(page.locator("#statusServer")).toHaveText("연결 실패");
  await preset.selectOption("mock");
  await expect(page.locator("input[name=api_key]")).toHaveAttribute("placeholder", "프리셋 키 사용");
  await expect(page.locator("#statusServer")).toHaveText("sglang 연결됨");
});

test("설정 저장과 새로고침 뒤 유지", async () => {
  await page.locator("input[name=temperature]").fill("0.3");
  await expect(page.locator("#saveStatus")).toHaveText("저장하지 않은 변경이 있습니다");
  await page.locator("button[form=settings]").click();
  await expect(page.locator("#saveStatus")).toHaveText("저장했습니다");
  await page.reload();
  await expect(page.locator("input[name=temperature]")).toHaveValue("0.3");
  const cfg = readFileSync(join(tmp, ".env.toml"), "utf8");
  expect(cfg).toContain('temperature = "0.3"');
  expect(cfg).toContain("[preset.mock]"); // 저장해도 프리셋은 남는다
});

test("로그 탭: 요청 원문이 보이고 키는 없다", async () => {
  await tab("logs");
  const row = page.locator("#logList .logrow.kind-llm", { hasText: "/chat/completions" }).first();
  await expect(row).toBeVisible();
  await row.locator("summary").click();
  await expect(row.locator("pre")).toContainText('"chat_template_kwargs"');
  expect(await page.content()).not.toContain("e2e-secret-key");
});

test("기록 탭: 호출과 시나리오가 쌓인다", async () => {
  await tab("history");
  const tiles = page.locator("#histBody .tile");
  await expect(tiles.nth(0)).toContainText("LLM 호출");
  await expect(tiles.nth(1)).toContainText("시나리오 실행2회");
  await expect(page.locator("#histBody .dtable").first()).toContainText("mock-model");
  const files = readdirSync(join(tmp, "runs")).filter((f) => f.endsWith(".jsonl"));
  expect(files.length).toBe(1);
  expect(existsSync(join(tmp, "runs", files[0]))).toBe(true);
});

test("설정 패널 접기·펴기 (버튼, 새로고침 유지, Ctrl+B)", async () => {
  const app = page.locator(".app");
  const toggle = page.locator("#openSidebar");
  await expect(page.locator("#sidebar")).toBeVisible();
  await toggle.click();
  await expect(app).toHaveClass(/collapsed/);
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await page.reload();
  await expect(app).toHaveClass(/collapsed/);
  await page.keyboard.press("Control+B");
  await expect(app).not.toHaveClass(/collapsed/);
  await expect(page.locator("#sidebar")).toBeVisible();
});

test("테마는 새로고침 뒤에도 유지된다", async () => {
  const btn = page.locator("#themeBtn");
  for (let i = 0; i < 3 && (await page.evaluate(() => document.documentElement.dataset.theme)) !== "dark"; i++) {
    await btn.click();
  }
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.evaluate(() => localStorage.removeItem("theme"));
});
