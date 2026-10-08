# onprem-llm-testbed

온프렘 LLM 서버(sglang·vLLM·Ollama 등 OpenAI 호환 `/v1/chat/completions`)를 폐쇄망에서 점검하는 테스트 하네스.

| 할 수 있는 것 | 방법 |
|---|---|
| 한 번 호출·스트리밍 | `llmtest.exe -p "안녕하세요"` / `-stream` |
| 멀티턴 대화 | `-chat` |
| 언어별 멀티턴 시나리오 자동 검증 (한·영·일·중) | `-scenario ..\..\scenarios` |
| thinking on/off·reasoning_effort 비교 | `-think on\|off`, `-effort` (서버 종류에 맞춰 자동 변환) |
| 화면에서 설정 변경·대화·시나리오 실행 | `-web` 또는 `llmtest-web.bat` |

```
harness/
├── .env.toml.example   접속 설정 (복사해서 .env.toml)
├── scenarios/          멀티턴 시나리오 (JSON)
├── go/                 Go 클라이언트·웹 UI, bin/llmtest.exe (Windows 64비트, Go 설치 불필요)
└── python/             Python 클라이언트 (표준 라이브러리만)
```

시작은 [harness/README.md](harness/README.md), Windows 실행·빌드는 [harness/go/README.md](harness/go/README.md).
