# LLM 호출 테스트

sglang 등 OpenAI 호환 API(`/v1/chat/completions`)를 언어별 클라이언트로 호출해 본다.
두 클라이언트는 플래그·설정파일·시나리오 형식이 같다. 둘 다 표준 라이브러리만 써서 폐쇄망에서도 돈다.

```
test/
├── .env.toml.example   공통 접속 설정 (복사해서 .env.toml 로 사용, git 에 안 올림)
├── scenarios/          멀티턴 시나리오 (JSON, 언어별)
├── go/                 Go 클라이언트 + Windows 실행파일(bin/llmtest.exe)  → go/README.md
└── python/             Python 클라이언트 (3.8 이상)                       → python/README.md
```

## 접속 설정

```bat
cd test
copy .env.toml.example .env.toml
notepad .env.toml
```

`test/.env.toml` 하나를 두면 `go/`, `go/bin/`, `python/` 어디서 실행해도 찾는다
(현재 폴더와 실행파일·스크립트 폴더에서 각각 상위 2단계까지 찾는다).

## 실행 모드

| 모드 | Go | Python |
|---|---|---|
| 한 번 호출 | `llmtest.exe -p "안녕"` | `python llmtest.py -p "안녕"` |
| 대화형 멀티턴 | `llmtest.exe -chat` | `python llmtest.py -chat` |
| 시나리오 멀티턴 | `llmtest.exe -scenario ..\..\scenarios` | `python llmtest.py -scenario ..\scenarios` |
| 모델 목록 | `llmtest.exe -models` | `python llmtest.py -models` |

공통 플래그: `-stream`, `-think on|off|auto`, `-effort low|medium|high`, `-hide-think`, `-sys`, `-t`, `-max`, `-timeout`.
우선순위: **플래그 > 환경변수 > .env.toml**.

### 대화형 멀티턴 (`-chat`)

입력한 질문과 답변을 히스토리에 쌓아 매 턴 전체 대화를 보낸다. 대화 중 명령:

| 명령 | 동작 |
|---|---|
| `/think on\|off\|auto` | 다음 턴부터 thinking 모드 변경 |
| `/effort low\|medium\|high\|none` | reasoning_effort 변경 (`none` 은 안 보냄) |
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

## thinking(추론) 조절

| 설정 | 요청에 들어가는 값 |
|---|---|
| `-think on` | `"chat_template_kwargs": {"thinking": true, "enable_thinking": true}` |
| `-think off` | `"chat_template_kwargs": {"thinking": false, "enable_thinking": false}` |
| `-think auto` (기본) | 안 보냄 → 서버·모델 기본값 |
| `-effort high` | `"reasoning_effort": "high"` |

- 모델마다 채팅 템플릿 변수 이름이 달라서 두 이름을 같이 보낸다 (DeepSeek 계열 `thinking`, Qwen3·GLM 계열 `enable_thinking`). 템플릿이 쓰지 않는 변수는 무시된다.
- 추론 과정은 stderr 에 `<think> ... </think>` 로 출력하고 답변은 stdout 에 출력한다. `-hide-think` 로 숨긴다.
- 서버가 `reasoning_content` 를 따로 주지 않고 답변에 `<think>` 태그를 섞어 보내도 떼어 낸다.
- 멀티턴 히스토리에는 추론 과정을 넣지 않고 답변만 넣는다.
- 턴마다 `[소요 | 첫토큰 | 추론 N자 | 토큰 prompt= completion= reasoning=]` 를 출력한다. `-think off` 인데 추론 글자 수가 0 이 아니면 서버가 thinking 끄기를 무시한 것이다.
