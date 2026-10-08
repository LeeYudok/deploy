# LLM 테스트 하네스

sglang 등 OpenAI 호환 API(`/v1/chat/completions`)를 언어별 클라이언트로 호출해 본다.
두 클라이언트는 플래그·설정파일·시나리오 형식이 같다. 둘 다 표준 라이브러리만 써서 폐쇄망에서도 돈다.

```
harness/
├── .env.toml.example   공통 접속 설정 (복사해서 .env.toml 로 사용, git 에 안 올림)
├── scenarios/          멀티턴 시나리오 (JSON, 언어별)
├── go/                 Go 클라이언트·웹 UI + Windows 실행파일(bin/llmtest.exe) → go/README.md
└── python/             Python 클라이언트 (3.8 이상)                       → python/README.md
```

## 접속 설정

```bat
cd harness
copy .env.toml.example .env.toml
notepad .env.toml
```

`harness/.env.toml` 하나를 두면 `go/`, `go/bin/`, `python/` 어디서 실행해도 찾는다
(현재 폴더와 실행파일·스크립트 폴더에서 각각 상위 2단계까지 찾는다).

## 실행 모드

| 모드 | Go | Python |
|---|---|---|
| 한 번 호출 | `llmtest.exe -p "안녕"` | `python llmtest.py -p "안녕"` |
| 대화형 멀티턴 | `llmtest.exe -chat` | `python llmtest.py -chat` |
| 시나리오 멀티턴 | `llmtest.exe -scenario ..\..\scenarios` | `python llmtest.py -scenario ..\scenarios` |
| 모델 목록 | `llmtest.exe -models` | `python llmtest.py -models` |
| 웹 UI | `llmtest.exe -web` (또는 `llmtest-web.bat` 더블클릭) | - |

공통 플래그: `-server auto|sglang|vllm|ollama|openai`, `-stream`, `-think on|off|auto`, `-effort none|low|medium|high`, `-hide-think`, `-sys`, `-t`, `-max`, `-timeout`.
우선순위: **플래그 > 환경변수 > .env.toml**.

### 웹 UI (`-web`, Go 만)

`llmtest.exe -web` 을 실행하면 브라우저에 `http://127.0.0.1:8787/` 이 열린다. exe 하나에 화면(HTML/JS/CSS)이 들어 있어서 Node·인터넷이 필요 없다.

- **설정**: 접속주소·API 키·모델·thinking·reasoning_effort·temperature·max_tokens·시스템 프롬프트를 바꾼다. 화면 값은 바로 호출에 쓰이고, `.env.toml 에 저장` 을 누르면 파일에 쓴다. `목록` 은 `/v1/models` 로 모델을 불러온다.
- **대화**: 멀티턴 대화. 턴마다 thinking 을 바꿔 가며 보낼 수 있고, 추론 과정은 접힌 상자로, 소요·첫토큰·토큰 수는 답변 아래에 표시한다.
- **시나리오**: `scenarios/` 를 골라 돌리고 PASS/FAIL 표를 보여 준다. thinking 을 `모두 on` / `모두 off` 로 강제해 비교할 수 있다.
- 기본은 이 PC(127.0.0.1)에서만 열린다. 다른 PC 에서 보려면 `-addr 0.0.0.0:8787` 로 연다 (같은 망에서 누구나 이 화면으로 LLM 을 호출할 수 있게 되니 주의).
- API 키는 화면으로 내보내지 않는다. 저장된 키가 있으면 "저장된 키 사용 중" 만 표시한다.
- `-no-open` 은 브라우저를 열지 않는다. 저장 위치는 화면 위쪽에 나온다 (설정파일이 없으면 `harness/.env.toml` 에 만든다).

### 대화형 멀티턴 (`-chat`)

입력한 질문과 답변을 히스토리에 쌓아 매 턴 전체 대화를 보낸다. 대화 중 명령:

| 명령 | 동작 |
|---|---|
| `/think on\|off\|auto` | 다음 턴부터 thinking 모드 변경 |
| `/effort none\|low\|medium\|high` | reasoning_effort 변경 (값 없이 `/effort` 만 치면 안 보냄) |
| `/history` | 지금까지 보낸 대화 출력 |
| `/reset` | 대화 기록 초기화 (system 만 남김) |
| `/quit` | 종료 |

### 시나리오 멀티턴 (`-scenario`)

`scenarios/*.json` 을 차례로 돌리고 턴마다 기대 문자열을 검사한다.
마지막에 시나리오별 PASS/FAIL 표를 출력하고, 실패가 있으면 종료코드 2 로 끝난다(오류는 1).

| 파일 | 내용 |
|---|---|
| `ko-memory.json` / `en-memory.json` / `ja-memory.json` / `zh-memory.json` | 이름·숫자를 기억하고, 값을 바꾼 뒤에도 최신 값으로 답하는지 (한·영·일·중) |
| `ko-think-toggle.json` | 턴마다 thinking 을 켜고 끄면서 앞 턴 계산 결과를 이어 쓰는지 |

형식:

```json
{
  "name": "ko-memory",
  "system": "한국어로 짧게 답변하세요.",
  "think": "off",
  "turns": [
    {"user": "내 이름은 김민수야. 기억해 줘."},
    {"user": "내 이름이 뭐였지?", "expect": ["김민수|민수"]},
    {"user": "어려운 계산 문제 ...", "think": "on", "expect": ["31"]}
  ]
}
```

- `system`: 없으면 `-sys` 값을 쓴다.
- `think`: 시나리오 기본값. 턴의 `think` 가 있으면 그 턴만 바꾼다. 둘 다 없으면 `-think` 값을 쓴다.
  on/off 를 비교하려면 `think` 를 적지 않은 시나리오를 `-think on`, `-think off` 로 두 번 돌린다.
- `expect`: 답변에 **모두** 들어 있어야 PASS (대소문자 무시). `"a|b"` 는 둘 중 하나만 있어도 된다.

## 서버 종류와 thinking(추론) 조절

thinking 을 켜고 끄는 요청 필드가 서버마다 다르다. `-server auto`(기본)는 시작할 때 `/v1/models` 의 `owned_by` 로 서버를 판별하고 거기에 맞춰 보낸다.
판별 결과는 `[서버 sglang (owned_by=sglang)]` 처럼 출력되고, 턴마다 통계 줄에 `server=` 로 나온다.

| 서버 | `owned_by` | `-think on` | `-think off` |
|---|---|---|---|
| sglang | `sglang` | `chat_template_kwargs: {"thinking": true, "enable_thinking": true}` | 같은 키를 `false` 로 |
| vLLM | `vllm` | sglang 과 같음 | sglang 과 같음 |
| Ollama | `library` | 보내지 않음 (기본이 thinking) | `reasoning_effort: "none"` |
| 그 밖 | 그 외 | sglang 과 같음 | sglang 과 같음 |

- `-think auto` 는 아무것도 보내지 않으므로 결과가 서버 설정에 달려 있다. sglang 0.5.10 코드(`serving_chat.py` `_get_reasoning_from_request`)로는 DeepSeek 계열(`--reasoning-parser deepseek-v3`)은 `thinking: true` 를 명시해야만 추론하고, Qwen3·GLM 계열은 기본이 켜짐이다. 다만 서버 기동 옵션이나 템플릿에 따라 달라진다 — 아래 vllm.dev 실측은 Qwen3.8 인데도 auto 가 꺼짐이었다. **thinking 을 비교할 때는 auto 대신 on/off 를 명시한다.**
- 채팅 템플릿 변수는 모델마다 이름이 달라 두 개를 같이 보낸다 (DeepSeek·Kimi `thinking`, Qwen3·GLM `enable_thinking`). sglang 도 `reasoning_effort: "none"` 을 받으면 내부에서 이 두 키를 `false` 로 채운다. 템플릿이 쓰지 않는 변수는 무시된다.
- `-effort none|low|medium|high` 는 `reasoning_effort` 를 그대로 보낸다. 직접 정하면 `-think off` 가 덮어쓰지 않는다. sglang 은 0.5.10 부터 `none` 을 받는다 (그 전 버전은 400 이 날 수 있다).
- 판별이 틀리거나 `/v1/models` 가 막혀 있으면 `-server sglang` 처럼 직접 정한다 (`.env.toml` 의 `server`).
- 추론 필드는 `reasoning_content`(sglang, 구 vLLM)와 `reasoning`(Ollama, 신 vLLM)을 둘 다 읽는다. reasoning parser 없이 답변에 `<think>` 가 섞여 와도 떼어 낸다 (`<think>` 를 프롬프트에 붙이는 sglang 처럼 여는 태그가 없어도 된다).
- 추론 과정은 stderr 에 `<think> ... </think>` 로 출력하고 답변은 stdout 에 출력한다. `-hide-think` 로 숨긴다.
- 멀티턴 히스토리에는 추론 과정을 넣지 않고 답변만 넣는다.
- 턴마다 `[server= | 소요 | 첫토큰 | 추론 N자 | 토큰 prompt= completion= reasoning=]` 를 출력한다. `-think off` 인데 추론 글자 수가 0 이 아니면 서버가 thinking 끄기를 무시한 것이다.

실측 (2026-10-08):

| 서버 | 판별 | auto | `-think on` | `-think off` | 시나리오 5개 24턴 |
|---|---|---|---|---|---|
| sglang `RedHatAI/Qwen3.8-27B-INT4` (vllm.dev.doksam.com) | `sglang` | 추론 0자, 152ms | 추론 143자, 598ms | 추론 0자, 127ms | on·off 모두 PASS |
| Ollama `qwen3.8:27b` (맥 로컬) | `ollama` | 추론 있음 | 추론 144자 | 추론 0자, completion 3 토큰 | PASS |
