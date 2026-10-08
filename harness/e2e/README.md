# 웹 UI E2E (Playwright)

하네스 웹 UI 를 브라우저로 띄워 대화·thinking·중지·코드 블록·시나리오·프리셋·저장·로그·기록·테마·모바일 화면을 검사한다.
LLM 서버 대신 **모의 LLM**(`mock-llm.mjs`)을 띄우므로 실서버나 인터넷 없이 같은 결과가 나온다.

```
e2e/
├── playwright.config.mjs   브라우저·서버 설정
├── mock-llm.mjs            모의 LLM (OpenAI 호환, owned_by=sglang, Node 표준 모듈만)
├── start-harness.mjs       임시 .env.toml·기록 폴더(.tmp/)를 만들고 하네스 웹 UI 를 띄움
├── fixtures/scenarios/     테스트용 시나리오 (통과 1개, 실패 1개)
└── tests/                  harness.spec.mjs (데스크톱), mobile.spec.mjs (390px)
```

테스트는 `.tmp/` 의 임시 설정과 기록 폴더만 쓴다. 실제 `harness/.env.toml` 과 `harness/runs/` 는 건드리지 않는다.

## 실행

```sh
cd harness/e2e
npm install                 # 처음 한 번 (폐쇄망은 아래 참고)
npx playwright test         # 헤드리스
E2E_HEADED=1 npx playwright test   # 브라우저 창을 띄워서 본다 (창 최대화, Windows 는 set E2E_HEADED=1)
npx playwright show-report  # 결과 보고서 (실패하면 trace 포함)
```

| 환경변수 | 기본값 | 설명 |
|---|---|---|
| `E2E_CHANNEL` | Windows: `msedge`, 그 밖: Playwright Chromium | 설치된 브라우저를 쓴다 (`msedge`, `chrome`) |
| `E2E_HEADED` | 없음 | `1` 이면 창을 띄운다 |
| `HARNESS_BIN` | 없음 (`go build`) | 이미 빌드한 `llmtest(.exe)` 를 쓴다. Go 가 없는 PC 용 |
| `MOCK_PORT` / `HARNESS_PORT` | 18901 / 18902 | 포트가 겹치면 바꾼다 |

Windows 명령 프롬프트 예:

```bat
cd harness\e2e
set HARNESS_BIN=%CD%\..\go\bin\llmtest.exe
npx playwright test
```

## 폐쇄망에 설치하기

Playwright 는 **라이브러리**(npm 패키지)와 **브라우저**가 필요하다. 인터넷이 되는 PC 에서 미리 받아서 옮긴다.

1. **브라우저는 설치된 Edge 를 쓴다.** Windows 에는 Edge 가 있으므로 브라우저를 따로 받지 않아도 된다 (`E2E_CHANNEL=msedge`, Windows 기본값).
2. **라이브러리**: 인터넷 PC(쓸 PC 와 같은 OS·아키텍처)에서 받아 `node_modules` 째 옮긴다.

   ```bat
   cd harness\e2e
   set PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
   npm ci
   ```

   `harness\e2e` 폴더 전체(`node_modules` 포함)를 폐쇄망 PC 의 같은 위치로 복사한다. 사내 npm 미러(Nexus·Verdaccio 등)가 있으면 그걸로 `npm ci` 해도 된다.
3. **Node.js**: 폐쇄망 PC 에 Node 18 이상이 있어야 한다. 없으면 nodejs.org 의 Windows zip 을 받아 풀고 PATH 에 넣는다.
4. Playwright 전용 Chromium 이 꼭 필요하면 인터넷 PC 에서 `npx playwright install chromium` 으로 받은 `%LOCALAPPDATA%\ms-playwright` 폴더를 옮기고 `PLAYWRIGHT_BROWSERS_PATH` 로 가리킨다.

**버전 맞추기**: `@playwright/test` 는 `1.63.0` 으로 고정돼 있다. 폐쇄망 Edge 가 아주 오래돼 실행이 안 되면, 그 Edge 버전을 지원하던 Playwright 버전으로 `package.json` 을 낮춘다.

## 모의 LLM 이 하는 답

| 질문 | 답 |
|---|---|
| `ping` | `pong` |
| `내 이름은 X야` → `내 이름이 뭐였지?` | `X 입니다.` (앞 턴 기억) |
| `6*7` | `42` |
| `코드` | `main.go:` 다음에 Go 코드 블록 |
| `천천히` | 6초 동안 느리게 스트리밍 (중지 테스트) |
| `빈본문` | 추론만 주고 본문은 비움 |

`chat_template_kwargs.thinking` 이 `true` 면 `reasoning_content` 를 먼저 보낸다 (sglang deepseek-v3 파서와 같다). `GET /__requests` 로 받은 요청 본문과 Authorization 헤더를 볼 수 있다.
