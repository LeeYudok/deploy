# onprem-llm-testbed

사내(온프렘) LLM 서버를 폐쇄망 안에서 점검하는 테스트 하네스다.
sglang·vLLM·Ollama 처럼 OpenAI 호환 `/v1/chat/completions` 를 여는 서버라면 붙일 수 있다.

Windows PC 에 실행파일 하나(`llmtest.exe`)만 복사하면 된다. Go·Node·Python 을 설치하지 않아도 되고 인터넷도 필요 없다.
클라이언트는 Go 와 Python 둘 다 표준 라이브러리만 쓴다. 그래서 폐쇄망에서도 소스를 다시 빌드할 수 있다.

## 할 수 있는 것

| 기능 | 내용 |
|---|---|
| 한 번 호출 | 스트리밍 여부, 소요·첫 토큰(TTFT)·토큰 수 표시 |
| 멀티턴 대화 | 앞선 대화를 매 턴 함께 보낸다. 추론 과정은 히스토리에서 뺀다 |
| 시나리오 검증 | JSON 으로 적은 여러 턴을 돌리고, 기대 문자열로 PASS/FAIL 을 판정한다. 한·영·일·중 시나리오 포함 |
| thinking 조절 | `-think on/off` 를 서버에 맞는 필드로 바꿔 보낸다. 서버 종류는 `/v1/models` 로 자동 판별한다 |
| 웹 UI | 설정·프리셋, 대화, 시나리오, 실행 기록 비교, 요청·응답 로그를 브라우저에서 다룬다. 코드 블록은 복사하거나 파일로 내려받을 수 있다 |
| 실행 기록 | 모든 호출과 시나리오 결과를 JSONL 로 남긴다. 모델·thinking 별 속도와 통과율을 비교한다 |

## 빠른 시작 (Windows)

```bat
cd harness
copy .env.toml.example .env.toml
notepad .env.toml
```

`.env.toml` 에 접속주소와 모델을 적는다.

```toml
[llm]
base_url = "http://<HOST>:<PORT>/v1"
model = "<모델 id>"
api_key = ""          # 필요한 경우만
```

웹 UI 로 쓴다면 `harness\go\bin\llmtest-web.bat` 을 더블클릭한다. 브라우저에 `http://127.0.0.1:8787/` 이 열린다.

명령 프롬프트에서 쓴다면 이렇게 한다.

```bat
cd harness\go\bin
llmtest.exe -models
llmtest.exe -p "안녕하세요"
llmtest.exe -think off -p "17*23은?"
llmtest.exe -chat
llmtest.exe -scenario ..\..\scenarios
```

위에서부터 모델 목록, 한 번 호출, thinking 을 끄고 호출, 대화형 멀티턴, 시나리오 검증이다. 시나리오에 실패가 있으면 종료코드 2 로 끝난다.

한글이 깨지면 `chcp 65001` 을 먼저 실행한다.

## 구성

```
harness/
├── .env.toml.example   접속 설정 예시. 복사해서 .env.toml 로 쓴다 (git 에 안 올림)
├── scenarios/          멀티턴 시나리오 (JSON)
├── go/                 Go 클라이언트와 웹 UI
│   └── bin/            llmtest.exe (Windows 64비트), llmtest-web.bat, Go 1.27.1 배포판 zip
├── python/             Python 클라이언트 (3.8 이상, 표준 라이브러리만)
├── e2e/                웹 UI Playwright E2E (모의 LLM)
└── runs/               실행 기록 (자동 생성, git 에 안 올림)
scripts/
├── dev-web.sh          개발용: 소스가 바뀌면 웹 UI 를 다시 빌드하고 재시작
└── commit-msg.sh       커밋 메시지 형식 검사 hook
```

자세한 사용법은 [harness/README.md](harness/README.md), Windows 빌드는 [harness/go/README.md](harness/go/README.md), Python 은 [harness/python/README.md](harness/python/README.md) 에 있다.

## 서버별 thinking 조절

thinking 을 켜고 끄는 필드는 서버마다 다르다. 하네스는 `-server auto`(기본)로 서버를 판별하고 거기에 맞춰 보낸다.

| 서버 | `-think on` | `-think off` |
|---|---|---|
| sglang · vLLM | `chat_template_kwargs: {"thinking": true, "enable_thinking": true}` | 같은 키를 `false` 로 |
| Ollama | 보내지 않음 (기본이 켜짐) | `reasoning_effort: "none"` |

`-think auto` 는 아무것도 보내지 않는다. 그래서 결과가 서버 설정에 따라 달라진다. sglang 의 DeepSeek 계열은 `thinking: true` 를 명시해야만 추론한다. thinking 을 비교할 때는 auto 대신 on/off 를 명시한다.

## 폐쇄망에서 쓰기

- 실행: `harness/go/bin/llmtest.exe` 하나면 된다. 웹 UI 화면도 exe 안에 들어 있다.
- 다시 빌드: `harness/go/bin/go1.27.1.windows-amd64.zip` 을 풀어 PATH 에 넣고 `go build` 한다. 외부 모듈이 없으므로 다운로드가 필요 없다.
- 접속주소와 API 키는 `.env.toml` 에만 둔다. 이 파일은 git 에 올리지 않는다. 키는 그 키가 적힌 접속주소로만 보내고, 로그와 화면에는 나오지 않는다.
- 웹 UI 는 기본으로 이 PC(127.0.0.1)에서만 열린다. 다른 PC 에서 보려면 `-addr 0.0.0.0:8787` 로 연다.
- E2E 를 폐쇄망에서 돌리는 방법은 [harness/e2e/README.md](harness/e2e/README.md) 에 있다 (설치된 Edge 사용).

## 개발

```sh
scripts/dev-web.sh                    # 소스가 바뀌면 웹 UI 를 다시 빌드하고 재시작한다 (127.0.0.1:8787)
cd harness/go && go vet ./... && go test ./...
cd harness/e2e && npx playwright test # 웹 UI E2E 13개
```

`harness/go` 를 고치면 `bin/llmtest.exe` 와 `bin/SHA256SUMS` 를 Go 1.27.1 로 다시 빌드한다.

```sh
cd harness/go
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.27.1 go build -trimpath -ldflags "-s -w" -o bin/llmtest.exe .
```

## 라이선스

[MIT](LICENSE)

함께 들어 있는 서드파티 구성요소는 각자의 라이선스를 따른다.

| 구성요소 | 위치 | 라이선스 |
|---|---|---|
| Go 배포판 (go1.27.1.windows-amd64.zip) | `harness/go/bin/` | BSD-3-Clause ([go.dev/LICENSE](https://go.dev/LICENSE)) |
| Phosphor Icons | `harness/go/web/icons.js` (path 인라인) | MIT ([phosphor-icons/core](https://github.com/phosphor-icons/core)) |
| Playwright (`@playwright/test`) | `harness/e2e` 개발 의존성 (레포에 넣지 않음) | Apache-2.0 |

---

made by doksam
