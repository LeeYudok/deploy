// E2E 용 하네스 웹 UI 를 띄운다. 실제 .env.toml·runs/ 를 건드리지 않도록 .tmp/ 에 임시 설정과 기록 폴더를 만든다.
//
//   HARNESS_BIN=...\llmtest.exe   이미 빌드한 실행파일을 쓴다 (폐쇄망 PC 처럼 Go 가 없을 때)
//   (없으면)                      ../go 를 .tmp/ 에 빌드해서 쓴다
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const tmp = join(here, ".tmp");
const mockPort = Number(process.env.MOCK_PORT || 18901);
const port = Number(process.env.HARNESS_PORT || 18902);
const mock = `http://127.0.0.1:${mockPort}/v1`;

rmSync(tmp, { recursive: true, force: true });
mkdirSync(join(tmp, "runs"), { recursive: true });
const cfg = join(tmp, ".env.toml");
writeFileSync(cfg, `[llm]
base_url = "${mock}"
model = "mock-model"
api_key = "e2e-secret-key"
server = "auto"
think = "auto"
temperature = "0.7"
max_tokens = "512"
system = "E2E 테스트"

[preset.mock]
label = "E2E mock"
base_url = "${mock}"
model = "mock-model"
server = "sglang"
api_key = "e2e-secret-key"

[preset.other]
label = "닿지 않는 서버"
base_url = "http://127.0.0.1:9/v1"
model = "other-model"
`);

let bin = process.env.HARNESS_BIN;
if (!bin) {
  bin = join(tmp, process.platform === "win32" ? "llmtest.exe" : "llmtest");
  execFileSync("go", ["build", "-o", bin, "."], { cwd: join(here, "..", "go"), stdio: "inherit" });
}
const child = spawn(bin, [
  "-web", "-no-open", "-addr", `127.0.0.1:${port}`,
  "-config", cfg, "-scenario", join(here, "fixtures", "scenarios"), "-runs", join(tmp, "runs"),
], { stdio: "inherit" });
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => child.kill(sig));
child.on("exit", (code) => process.exit(code ?? 0));
