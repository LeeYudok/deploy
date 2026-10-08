# LLM 호출 테스트 (Python)

Go 클라이언트와 같은 플래그·설정파일·시나리오를 쓰는 Python 판이다.
표준 라이브러리만 쓰므로 `pip install` 없이 Python 3.8 이상에서 바로 돈다.
멀티턴·thinking 조절·시나리오 형식은 [상위 README](../README.md) 참고.

```bat
cd test
copy .env.toml.example .env.toml
notepad .env.toml
cd python
python llmtest.py -models
python llmtest.py -p "안녕하세요"
python llmtest.py -stream -think off -p "Python 장점 3가지"
python llmtest.py -chat
python llmtest.py -scenario ..\scenarios
```

- 한글이 깨지면 `chcp 65001` 후 다시 실행하거나 `set PYTHONUTF8=1` 을 준다.
- 프록시를 타서 접속이 안 되면 `set HTTP_PROXY=` / `set NO_PROXY=<HOST>` 로 확인한다.
