# LLM 호출 테스트 (Python)

Go 클라이언트와 같은 플래그·설정파일·시나리오·실행 기록 형식을 쓰는 Python 판이다 (웹 UI 만 Go 전용).
표준 라이브러리만 쓰므로 `pip install` 없이 Python 3.8 이상에서 바로 돈다.
멀티턴·thinking 조절·시나리오 형식은 [상위 README](../README.md) 참고.

```bat
cd harness
copy .env.toml.example .env.toml
notepad .env.toml
cd python
python llmtest.py -models
python llmtest.py -p "안녕하세요"
python llmtest.py -stream -think off -p "Python 장점 3가지"
python llmtest.py -chat
python llmtest.py -scenario ..\scenarios
python llmtest.py -preset dev -p "ping"
python llmtest.py -bench 1,2,4 -bench-requests 8 -max 128 -think off
```

- 호출·시나리오·부하 테스트 결과를 `harness/runs/YYYY-MM-DD.jsonl` 에 Go 와 같은 형식으로 남긴다. 끄려면 `-no-record`, 폴더를 바꾸려면 `-runs <폴더>`. 웹 UI 기록 탭에서 함께 보인다.
- `.env.toml` 에 `[price."<모델>"]` 단가가 있으면 통계 줄에 `환산 $…` 가 붙는다.
- 없는 프리셋을 주면 있는 프리셋을 `order` 순으로 보여 준다.

- 한글이 깨지면 `chcp 65001` 후 다시 실행하거나 `set PYTHONUTF8=1` 을 준다.
- 프록시를 타서 접속이 안 되면 `set HTTP_PROXY=` / `set NO_PROXY=<HOST>` 로 확인한다.
