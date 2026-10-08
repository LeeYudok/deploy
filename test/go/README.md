# LLM 호출 테스트 (Go)

sglang 등 OpenAI 호환 API(`/v1/chat/completions`)를 호출하는 테스트 클라이언트.
표준 라이브러리만 사용하므로 외부 모듈 다운로드 없이(폐쇄망에서도) 빌드된다.
멀티턴·thinking 조절·시나리오 형식은 [상위 README](../README.md) 참고.

## Windows 64비트에서 바로 실행 (Go 설치 불필요)

`bin/llmtest.exe` 는 미리 빌드한 Windows 64비트 실행파일이다(Go 1.27.1, CGO 없음).
`test` 폴더에 `.env.toml` 을 만들고 바로 실행한다 (exe 폴더에서 상위 2단계까지 찾는다).

```bat
cd test
copy .env.toml.example .env.toml
notepad .env.toml
cd go\bin
llmtest.exe -models
llmtest.exe -p "안녕하세요"
llmtest.exe -think off -p "안녕하세요"
llmtest.exe -scenario ..\..\scenarios
llmtest.exe -chat
llmtest.exe -web
```

`bin/llmtest-web.bat` 을 더블클릭하면 웹 UI 가 바로 열린다.

> 소스를 바꿨으면 아래 4번 또는 Linux/Mac 에서 다시 빌드해 `bin/llmtest.exe` 를 갱신한다:
> `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/llmtest.exe .`

## Windows 64비트에서 소스로 실행

### 1. Go 설치

**설치 없이 쓰기 (폐쇄망 권장)**: `bin/go1.27.1.windows-amd64.zip` 을 원하는 폴더(예: `D:\`)에 압축 해제하고 PATH 에 추가한다.
`go.exe` 하나만 복사하면 빌드되지 않는다. 압축을 푼 `go` 폴더 전체(`pkg\tool`, `src` 포함)가 있어야 한다.

```bat
set PATH=D:\go\bin;%PATH%
set GOTOOLCHAIN=local
go version
```

무결성 확인: `certutil -hashfile go1.27.1.windows-amd64.zip SHA256` 결과가 `bin/SHA256SUMS` 와 같아야 한다.

**설치판 쓰기**:

1. https://go.dev/dl/ 에서 **`go1.xx.x.windows-amd64.msi`** 다운로드 (1.21 이상)
   - 인터넷이 안 되는 PC는 다른 PC에서 받아서 옮긴 뒤 설치
2. 설치 후 **새로 연** 명령 프롬프트에서 확인

```bat
go version
```

`go version go1.xx.x windows/amd64` 처럼 `windows/amd64` 가 나오면 된다.

### 2. 접속 설정 (.env.toml)

접속주소는 git 에 올리지 않는다. 예제 파일을 복사해서 직접 입력한다.

```bat
cd test
copy .env.toml.example .env.toml
notepad .env.toml
```

```toml
[llm]
base_url = "http://<HOST>:<PORT>/v1"   # 실제 주소로 수정
model = "deepseek-v4-flash-0731"
api_key = ""                           # 필요한 경우만
think = "auto"                         # on / off / auto
reasoning_effort = ""                  # low / medium / high
```

> 메모장에서 저장할 때 인코딩은 **UTF-8** 로 저장한다.

### 3. 실행

```bat
cd test\go
go run . -models
go run . -p "안녕하세요"
go run . -stream -p "Go 언어 장점 3가지"
go run . -think off -p "안녕하세요"
go run . -scenario ..\scenarios
go run . -chat
```

### 4. exe 빌드 (64비트)

```bat
set GOOS=windows
set GOARCH=amd64
go build -o llmtest.exe .
```

PowerShell 이면:

```powershell
$env:GOOS="windows"; $env:GOARCH="amd64"
go build -o llmtest.exe .
```

빌드된 `llmtest.exe` 는 Go 가 없는 PC에서도 실행된다.
`.env.toml` 은 **exe 폴더나 현재 폴더, 또는 그 상위 2단계 안**에 두면 된다.
웹 UI 화면(`web/`)은 `go:embed` 로 exe 안에 들어가므로 exe 하나만 옮기면 된다.

```bat
llmtest.exe -models
llmtest.exe -p "안녕하세요"
llmtest.exe -config D:\conf\llm.toml -p "다른 설정파일 사용"
```

> Linux/Mac 에서 Windows 64비트용 exe 만들기:
> `GOOS=windows GOARCH=amd64 go build -o llmtest.exe .`

## 옵션

| 플래그 | 환경변수 | .env.toml 키 | 기본값 / 설명 |
|---|---|---|---|
| `-url` | `LLM_BASE_URL` | `base_url` | 필수 |
| `-model` | `LLM_MODEL` | `model` | 필수 (`-models` 조회 시 제외) |
| `-key` | `LLM_API_KEY` | `api_key` | 있으면 `Authorization: Bearer` 헤더 추가 |
| `-config` | | | 설정파일 경로 (기본: 현재 폴더 → exe 폴더, 각각 상위 2단계까지의 `.env.toml`) |
| `-p` | | | 사용자 프롬프트 |
| `-sys` | | | 시스템 프롬프트 |
| `-t` | | | temperature (0.7) |
| `-max` | | | max_tokens (1024) |
| `-stream` | | | 스트리밍 출력 |
| `-models` | | | 모델 목록만 조회 |
| `-timeout` | | | 요청 타임아웃 (300s) |
| `-think` | `LLM_THINK` | `think` | `on` / `off` / `auto`(기본, 안 보냄) |
| `-effort` | `LLM_REASONING_EFFORT` | `reasoning_effort` | `low` / `medium` / `high` (비우면 안 보냄) |
| `-hide-think` | | | 추론 과정 출력 숨김 |
| `-chat` | | | 대화형 멀티턴 모드 |
| `-scenario` | | | 시나리오 JSON 파일·폴더 (쉼표로 여러 개) |
| `-web` | | | 웹 UI 실행 (설정 변경·대화·시나리오) |
| `-addr` | | | 웹 UI 주소 (`127.0.0.1:8787`) |
| `-no-open` | | | 웹 UI 실행 시 브라우저를 열지 않음 |
| | `LLM_TEMPERATURE` | `temperature` | `-t` 기본값 |
| | `LLM_MAX_TOKENS` | `max_tokens` | `-max` 기본값 |
| | `LLM_SYSTEM` | `system` | `-sys` 기본값 |

우선순위: **플래그 > 환경변수 > .env.toml**

## 문제 해결

- **한글이 깨짐**: 명령 프롬프트에서 `chcp 65001` 실행 후 다시 실행
- **`ERROR: 접속주소 없음`**: `.env.toml` 을 못 찾았거나(현재 폴더·exe 폴더와 각 상위 2단계) `base_url` 이 비어 있음
- **연결 시간 초과 / 거부**: 사내망 연결, 프록시 설정(`set HTTP_PROXY=` 로 해제) 확인
- **`'go'은(는) ... 아닙니다`**: Go 설치 후 명령 프롬프트를 새로 열었는지 확인
