# LLM 테스트 하네스

sglang 등 OpenAI 호환 API(`/v1/chat/completions`)를 언어별 클라이언트로 호출해 본다.
두 클라이언트는 플래그·설정파일·시나리오 형식이 같다. 둘 다 표준 라이브러리만 써서 폐쇄망에서도 돈다.

```
harness/
├── .env.toml.example   공통 접속 설정 (복사해서 .env.toml 로 사용, git 에 안 올림)
├── scenarios/          멀티턴 시나리오 (JSON, 언어별)
├── go/                 Go 클라이언트·웹 UI + Windows 실행파일(bin/llmtest.exe) → go/README.md
├── e2e/                웹 UI Playwright E2E (모의 LLM, 폐쇄망 설치 안내)       → e2e/README.md
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

### 접속 프리셋

서버가 여러 대면 `.env.toml` 에 `[preset.<id>]` 섹션으로 적어 둔다. 웹 UI 의 **프리셋** 콤보에서 고르면 주소·모델·서버 종류·키가 한 번에 바뀌고, CLI 는 `-preset <id>` 로 쓴다.
접속주소와 키가 들어가므로 프리셋은 `.env.toml`(git 에 안 올림)에만 둔다.

```toml
[preset.dev]
label = "개발 sglang"
base_url = "http://<HOST>:<PORT>/v1"
model = "<모델 id>"
server = "sglang"      # 생략하면 auto
api_key = ""           # 필요한 경우만
think = ""             # 고르면 thinking 도 이 값으로
```

- 우선순위: **플래그 > `-preset` > 환경변수 > `[llm]`**
- 웹 UI 의 모델 콤보에는 서버가 알려 준 모델과 프리셋 모델이 함께 나온다.
- 저장된 키는 **그 키가 적힌 접속주소로만** 보낸다. 화면에서 주소를 바꾸면 다른 서버의 키가 따라가지 않는다.
- 웹 UI 에서 `.env.toml 에 저장` 을 눌러도 프리셋 섹션은 그대로 남는다 (`[llm]` 만 바뀐다).

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

- **설정(왼쪽)**: 프리셋·접속주소·서버 종류·API 키·모델·temperature·max_tokens·reasoning_effort·스트리밍·시스템 프롬프트. 화면 값은 바로 호출에 쓰이고, `.env.toml 에 저장` 을 누르면 파일에 쓴다. 위쪽 상태 카드에 판별한 서버와 모델이 나오고, 새로고침 버튼으로 `/v1/models` 를 다시 부른다.
- **대화**: 멀티턴 대화. 입력창의 thinking `Auto / On / Off` 로 턴마다 바꿔 보낸다. 추론 과정은 스트리밍 중엔 펼쳐 보이고 끝나면 접히며, 답변 아래에 서버·think·소요·TTFT·토큰 칩과 답변 전체 복사 버튼이 붙는다.
- **코드 블록**: 머리줄에 언어·줄 수와 **복사**, **파일 다운로드** 버튼이 붙는다. 파일 이름은 펜스 정보(```` ```go main.go ````)나 블록 바로 앞 줄에 나온 이름(`main.go:`)이 언어 확장자와 맞으면 그 이름을, 아니면 `snippet-N.<확장자>` 를 쓴다. http 로 다른 PC 에서 열어 클립보드 API 가 막히면 다른 방식으로 복사한다.
- **시나리오**: `scenarios/` 를 카드로 골라 돌린다. thinking 을 `모두 On / Off / Auto` 로 강제해 비교할 수 있고, 검사 통과율·통과 시나리오·소요 요약 타일과 시나리오별 진행 막대, 턴별 로그를 보여 준다.
- **기록**: `runs/` 에 쌓인 실행 기록을 기간(오늘/7일/30일/전체)·모델·출처로 걸러 본다. 요약 타일, 모델·thinking 별 호출 비교(평균 소요·TTFT·completion 토큰·추론 글자 수·오류), 시나리오 통과율, 최근 시나리오 실행, 최근 호출(누르면 질문·답변·추론)을 보여 준다.
- **로그**: LLM 서버로 보낸 요청 JSON(`chat_template_kwargs`, `reasoning_effort`, messages)과 응답 요약(상태, 소요·TTFT, 토큰, 추론·본문 글자 수, finish, 오류 원문)을 줄마다 펼쳐 본다. 화면이 부른 `/api/*` 도 남는다. 종류 필터(전체/LLM/HTTP/오류)와 자동 스크롤이 있고, 다른 탭에 있을 때 오류가 생기면 탭에 빨간 점이 뜬다. 서버는 최근 500건을 메모리에 두고, 같은 줄을 터미널에도 `YYYY-MM-DD HH:MM:SS.mmm` 로 찍는다. API 키는 헤더로만 보내므로 로그에 남지 않는다.
- 라이트·다크·시스템 테마를 오른쪽 위 버튼으로 바꾼다. 화면은 폭 900px 아래에서 설정이 서랍으로 접힌다.
- 기본은 이 PC(127.0.0.1)에서만 열린다. 다른 PC 에서 보려면 `-addr 0.0.0.0:8787` 로 연다 (같은 망에서 누구나 이 화면으로 LLM 을 호출할 수 있게 되니 주의).
- API 키는 화면으로 내보내지 않는다. 키 칸에 "저장된 키 사용" / "프리셋 키 사용" 만 표시한다.
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

## 동시 처리 부하 테스트

서버가 동시 요청을 몇 건까지 감당하는지 잰다. 동시 수준마다 요청을 한꺼번에 보내고(스트리밍), 수준별로 다음을 낸다.

| 지표 | 뜻 |
|---|---|
| tok/s | 서버 전체가 1초에 내보낸 출력 토큰. 동시 수를 늘려도 이 값이 안 늘면 포화 |
| 요청당 tok/s | 요청 하나가 받는 출력 속도. 동시 수가 늘면 줄어든다 |
| req/s | 1초에 끝난 요청 수 |
| p50 / p95 / max | 요청 하나가 끝날 때까지 걸린 시간 |
| TTFT p50 / p95 | 첫 토큰까지 걸린 시간 |

**권장 동시 수**는 오류 없이 처리량(tok/s)이 앞 수준보다 10% 이상 늘어난 마지막 수준이다. 그 뒤로는 처리량이 거의 안 늘고 지연만 길어진다.

```bat
llmtest.exe -bench 1,2,4,8,16 -bench-requests 16 -max 128 -think off -p "다섯 문장으로 설명해 줘"
```

- 웹 UI 는 **부하** 탭에서 돌린다. 진행률, 요약 타일, 처리량·p95 지연 차트, 수준별 표를 보여 준다. 접속·모델·시스템 프롬프트는 왼쪽 설정을 쓴다.
- 요청은 Go 서버가 보낸다. 브라우저는 같은 호스트로 여는 연결 수가 제한돼 있어서 브라우저로는 동시 수를 높일 수 없다.
- 요청마다 프롬프트 끝에 번호를 붙여 응답 캐시 효과를 줄인다. 시스템 프롬프트 같은 공통 앞부분은 서버의 prefix 캐시가 그대로 쓴다.
- `-bench-requests` 를 비우면 수준마다 수준의 2배를 보낸다. 동시 수는 1~128.
- 결과는 실행 기록에 `type: "bench"` 로 남는다.
- 공유 서버에 부하가 간다. 작게 시작한다.

실측 (2026-10-09, sglang `Qwen3.8-27B-INT4`, max_tokens 128, think off, 수준당 16건):

| 동시 | tok/s | p95 | TTFT p95 |
|---|---|---|---|
| 1 | 83.6 | 1.65s | 91ms |
| 2 | 142.5 | 2.04s | 166ms |
| 4 | 229.9 | 2.38s | 137ms |
| **8 (권장)** | **350.7** | 2.98s | 289ms |
| 16 | 338.0 | 5.93s | 3.92s |

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
| sglang `RedHatAI/Qwen3.8-27B-INT4` (사내 개발 서버) | `sglang` | 추론 0자, 152ms | 추론 143자, 598ms | 추론 0자, 127ms | on·off 모두 PASS |
| Ollama `qwen3.8:27b` (맥 로컬) | `ollama` | 추론 있음 | 추론 144자 | 추론 0자, completion 3 토큰 | PASS |

### 팁: 시스템 프롬프트로 마크다운 끄기

"markdown 금지" 한 줄로는 잘 막히지 않는다. 모델이 글머리표(`-`)나 번호 목록(`1.`)을 마크다운으로 여기지 않고 그대로 쓴다.
금지할 문법을 하나씩 적고, 대신 쓸 형식을 알려 주면 막힌다.

실측 (2026-10-08, sglang `Qwen3.8-27B-INT4`, 질문 6개):

| 시스템 프롬프트 | think off | think on |
|---|---|---|
| `넌 최고 은행 30년차 개발자, markdown 으로 답변금지 …` | 6/6 답변에 마크다운 (글머리표 114, 번호 목록 47, 코드 펜스 2) | 2/6 (번호 목록 18) |
| 아래 개선본 | 0/6 | 0/6 |

~~~text
너는 은행에서 30년 일한 최고 수준의 개발자다.

답변은 일반 텍스트로만 쓴다. 마크다운 문법은 하나도 쓰지 않는다. 쓰면 안 되는 것: 줄 맨 앞의 # 제목, **굵게**, *기울임*, 줄 맨 앞의 - 나 * 글머리표, 줄 맨 앞의 '1.' 같은 번호 목록, | 로 만든 표, ``` 코드 펜스, 백틱(`), 줄 맨 앞의 > 인용.
여러 항목을 나열할 때는 '첫째,', '둘째,' 처럼 문장으로 쓰고 문단을 나눈다.
코드는 펜스나 백틱 없이 코드 그대로 쓰고, 들여쓰기는 공백 2칸으로 한다.
답변은 한국어로 한다. 사용자가 다른 언어를 요청할 때만 그 언어로 쓴다.
이모지를 쓰지 않는다.

답을 내기 전에 위 규칙을 어긴 곳이 없는지 확인한다.
~~~

코드 펜스를 막으면 웹 UI 의 코드 블록 복사·다운로드 버튼도 나오지 않는다. 코드를 자주 받는다면 "코드는 ``` 펜스로 감싼다" 를 예외로 두는 편이 낫다.

## 실행 기록 (`runs/`)

대화·시나리오·CLI 의 모든 LLM 호출과 시나리오 요약을 `harness/runs/YYYY-MM-DD.jsonl` 에 한 줄씩 덧붙인다 (git 에 안 올림, 파일 권한 600).

| type | 내용 |
|---|---|
| `turn` | 시각, 출처(`web-chat`/`web-scenario`/`cli`), 세션, 시나리오·턴 번호, 접속주소, 모델, 서버, think, effort, 질문, 답변, 추론, usage, TTFT, 소요, finish, 오류 |
| `scenario` | 시나리오 이름, 모델, thinking 강제값(`scenario` 면 시나리오 값), 턴·완료·검사·실패 수, 중지 여부, 소요, 턴별 검사 결과 |

- 중지한 턴은 남기지 않는다. 호출 오류는 남긴다.
- CLI 는 `-no-record` 로 끄고, `-runs <폴더>` 로 기록 폴더를 바꾼다 (웹 UI 도 같음). (Python 클라이언트는 아직 기록하지 않는다.)
- SQLite 대신 JSONL 을 쓴다: 표준 라이브러리만으로 되어 폐쇄망 빌드가 그대로다. 기록을 읽는 데 300ms, 쓰는 데 50ms 를 넘기면 로그 탭과 터미널에 `WARN runs 기록 읽기 느림 …` 이 찍힌다. 이 경고가 잦아지면 SQLite 로 옮길 때다.
- `jq` 로도 바로 볼 수 있다: `jq -r 'select(.type=="turn") | [.time,.model,.think,.elapsed_ms] | @tsv' runs/*.jsonl`

## 개발할 때 (자동 재배포)

```sh
scripts/dev-web.sh            # 레포 루트에서. 기본 127.0.0.1:8787
```

`harness/go` 의 `.go`·`web/`·`go.mod` 가 바뀌면 1초 안에 다시 빌드하고 웹 UI 를 재시작한다. 빌드가 실패하면 기존 서버를 그대로 두고 오류만 출력한다.
열려 있는 화면은 재시작을 알아채고 위쪽에 "새 버전이 배포됐습니다" 배너를 띄운다. 대화 중일 수 있어 자동으로 새로고침하지는 않는다.

웹 UI 를 고친 뒤에는 E2E 를 돌린다: `cd e2e && npx playwright test` (자세한 건 [e2e/README.md](e2e/README.md)).
