# AGENTS.md

> **기계 판독용 정보는 [`AGENTS.yaml`](AGENTS.yaml)** — 구성요소 그래프(`nodes`/`edges`), `commands`, `secret_files`(경로·키 이름만), `policies`, `known_issues` 등. 작업 전 이 파일을 먼저 읽고, 구성요소·경로·명령·환경변수가 바뀌면 이 문서와 **함께** 갱신한다. 두 파일이 어긋나면 실제 코드·파일시스템을 확인해 둘 다 고친다. 고친 뒤엔 `scripts/check-agents-yaml.sh` 로 검증한다. 날짜·버전 값은 항상 따옴표로 감싼다.

사내(온프렘) LLM 서버를 폐쇄망 Windows PC 에서 점검하는 하네스다. 개요는 [README.md](README.md), 설정·모드는 [harness/README.md](harness/README.md).

## 구조

| 경로 | 내용 |
|---|---|
| `harness/go/` | Go 클라이언트. CLI 와 웹 UI(`-web`) 서버. `web/` 은 `go:embed` 로 들어간다 |
| `harness/go/bin/` | 미리 빌드한 `llmtest.exe`, Go 1.27.1 Windows zip, `SHA256SUMS` |
| `harness/python/` | Python 클라이언트. 웹 UI 를 뺀 같은 기능 |
| `harness/scenarios/` | 멀티턴 시나리오 JSON |
| `harness/e2e/` | Playwright E2E 와 모의 LLM |
| `harness/.env.toml` | 접속·프리셋·단가 (gitignore, API 키 포함) |
| `harness/runs/` | 실행 기록 JSONL (gitignore) |

## 규칙

- **public 레포다.** 커밋·이슈·PR 에 사내 호스트명·IP 를 넣지 않는다. `<HOST>:<PORT>` 로 쓴다. 커밋 전에 `AGENTS.yaml` 의 `commands.checks.public_leak` 를 돌린다 (깨끗하면 0, 걸리면 1 로 끝나고 걸린 줄을 보여 준다).
- `harness/.env.toml` 은 출력하지 않는다. 키 이름만 필요하면 `grep -oE '^[a-z_]+ *='`.
- 클라이언트는 Go·Python 모두 표준 라이브러리만 쓴다. 폐쇄망에서 다시 빌드할 수 있어야 한다.
- 웹 UI 는 바닐라 HTML/JS + `go:embed` 다. Node 빌드를 넣지 않는다. 아이콘은 `web/icons.js` 의 Phosphor path 인라인이고 이모지는 쓰지 않는다.
- `harness/go` 를 고치면 `bin/llmtest.exe` 와 `SHA256SUMS` 를 `GOTOOLCHAIN=go1.27.1` 로 다시 빌드한다 (`commands.build.exe`).
- Go 클라이언트의 플래그나 기록 형식(`runs.go`)을 바꾸면 `harness/python/llmtest.py` 도 맞춘다.
- 웹 UI 를 고치면 `go test ./...` 와 E2E 를 돌린다. `@playwright/test` 는 1.63.0 고정.
- 브랜치 `main`. 작업은 `batch/claude-<YYYYMMDD>-<8자리 hex>` → PR(`Closes #N`) → merge commit.

## 알려진 함정

- 웹 UI 가 떠 있는 동안 `.env.toml` 을 직접 고치면, 옛 서버가 다음 저장 때 메모리 값으로 파일을 덮는다 (프리셋이 한 번 지워졌다). 설정은 실행 중인 웹 UI 의 `/api/config`, `/api/prices` 로 바꾼다.
- `[section]` 줄 뒤에 주석을 달면 하네스의 TOML 파서가 섹션을 못 읽는다. 주석은 따로 줄로 쓴다.
- OrcaRouter(orcarouter.ai)와 OpenRouter 는 다른 곳이다.
- 사용자가 Playwright 창을 함께 쓴다. 검증은 새 탭이나 헤드리스로 하고 해상도를 고정하지 않는다.
