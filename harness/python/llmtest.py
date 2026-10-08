#!/usr/bin/env python3
"""sglang(OpenAI 호환) LLM 호출 테스트 클라이언트 (Python, 표준 라이브러리만 사용)

사용법:
    python llmtest.py -p "안녕하세요"
    python llmtest.py -stream -think off -p "Python 장점 3가지"
    python llmtest.py -chat                     # 대화형 멀티턴
    python llmtest.py -scenario ../scenarios     # 시나리오 멀티턴 자동 검증
    python llmtest.py -models

접속 정보는 .env.toml 에서 읽는다 (../.env.toml.example 참고).
우선순위: 명령행 플래그 > 환경변수 > .env.toml > 기본값
Python 3.8 이상.
"""

import argparse
import ast
import glob
import json
import os
import sys
import time
import urllib.error
import urllib.request

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")


def load_toml(path):
    """key = "value" 형태의 단순 TOML 을 읽는다. [section] 이 있으면 키는 "section.key"."""
    cfg = {}
    section = ""
    with open(path, encoding="utf-8-sig") as f:
        for n, line in enumerate(f, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("[") and line.endswith("]"):
                section = line[1:-1].strip()
                continue
            if "=" not in line:
                raise ValueError("%s:%d: '=' 없음" % (path, n))
            k, v = (s.strip() for s in line.split("=", 1))
            if v.startswith('"'):
                end = v.rfind('"')
                if end == 0:
                    raise ValueError("%s:%d: 닫는 따옴표 없음" % (path, n))
                v = ast.literal_eval(v[: end + 1])
            elif "#" in v:
                v = v[: v.index("#")].strip()
            cfg[section + "." + k if section else k] = v
    return cfg


def find_config():
    """현재 폴더와 스크립트 폴더에서 시작해 상위 2단계까지 .env.toml 을 찾는다."""
    for start in (os.getcwd(), os.path.dirname(os.path.abspath(__file__))):
        d = start
        for _ in range(3):
            c = os.path.join(d, ".env.toml")
            if os.path.isfile(c):
                return c
            d = os.path.dirname(d)
    return ""


SERVERS = ("auto", "sglang", "vllm", "ollama", "openai")


def normalize_server(s):
    v = (s or "auto").strip().lower()
    if v not in SERVERS:
        raise ValueError("-server 값은 %s 중 하나: %r" % (" / ".join(SERVERS), s))
    return v


def server_from_owner(owner):
    """/v1/models 의 owned_by 로 서버 종류를 정한다 (sglang: "sglang", vLLM: "vllm", Ollama: "library")."""
    o = (owner or "").lower()
    if o == "sglang":
        return "sglang"
    if o == "vllm":
        return "vllm"
    if o in ("library", "ollama"):
        return "ollama"
    return "openai"


def parse_think(mode):
    """thinking 모드를 읽는다. None 이면 서버 기본값에 맡긴다."""
    m = (mode or "auto").strip().lower()
    if m == "auto":
        return None
    if m in ("on", "true", "1"):
        return True
    if m in ("off", "false", "0"):
        return False
    raise ValueError("-think 값은 on / off / auto 중 하나: %r" % mode)


def think_params(server, mode, effort):
    """thinking 모드를 서버에 맞는 요청 필드 (chat_template_kwargs, reasoning_effort) 로 바꾼다.

    sglang·vLLM·그 밖: chat_template_kwargs 에 thinking(DeepSeek·Kimi)과 enable_thinking(Qwen3·GLM)을 함께 보낸다.
    Ollama: OpenAI 호환 API 가 chat_template_kwargs 를 무시하므로 끌 때 reasoning_effort=none 을 보낸다.
    reasoning_effort 를 사용자가 정했으면 그 값을 그대로 둔다.
    """
    on = parse_think(mode)
    if on is None:
        return None, effort
    if server == "ollama":
        return None, (effort or ("" if on else "none"))
    return {"thinking": on, "enable_thinking": on}, effort


def split_think(s):
    """content 안의 <think>...</think> 블록을 떼어 낸다 (reasoning parser 가 없는 서버 대비)."""
    if "</think>" not in s:
        return "", s.strip()
    reasoning, content = s.split("</think>", 1)
    if "<think>" in reasoning:
        reasoning = reasoning.split("<think>", 1)[1]
    return reasoning.strip(), content.strip()


class Client:
    def __init__(self, args):
        self.base = args.url.rstrip("/")
        self.key = args.key
        self.model = args.model
        self.temp = args.t
        self.max_tokens = args.max
        self.stream = args.stream
        self.effort = args.effort
        self.hide_think = args.hide_think
        self.timeout = args.timeout
        self.server = args.server

    def _open(self, method, path, body=None):
        data = json.dumps(body).encode("utf-8") if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.key:
            req.add_header("Authorization", "Bearer " + self.key)
        if body is not None and body.get("stream"):
            req.add_header("Accept", "text/event-stream")
        try:
            return urllib.request.urlopen(req, timeout=self.timeout)
        except urllib.error.HTTPError as e:
            raise RuntimeError("HTTP %d: %s" % (e.code, e.read().decode("utf-8", "replace")))

    def list_models(self):
        with self._open("GET", "/models") as resp:
            return json.load(resp).get("data", [])

    def detect_server(self):
        """/v1/models 로 서버 종류를 정한다. 실패하면 openai 로 본다."""
        try:
            data = self.list_models()
        except (OSError, RuntimeError, ValueError) as e:
            self.server = "openai"
            sys.stderr.write("[서버 판별 실패, openai 로 진행: %s]\n" % e)
            return
        owner = ""
        for m in data:
            if m.get("id") == self.model or not owner:
                owner = m.get("owned_by") or ""
        self.server = server_from_owner(owner)
        sys.stderr.write("[서버 %s (owned_by=%s)]\n" % (self.server, owner))

    def models(self):
        for m in self.list_models():
            print("%s\t(owned_by=%s, max_model_len=%s)" % (m.get("id"), m.get("owned_by"), m.get("max_model_len")))

    def complete(self, messages, think):
        """messages 로 한 번 호출하고 답변을 출력한다. 추론 과정은 stderr 로 출력한다."""
        body = {
            "model": self.model,
            "messages": messages,
            "temperature": self.temp,
            "max_tokens": self.max_tokens,
            "stream": self.stream,
        }
        kw, effort = think_params(self.server, think, self.effort)
        if kw:
            body["chat_template_kwargs"] = kw
        if effort:
            body["reasoning_effort"] = effort
        if self.stream:
            body["stream_options"] = {"include_usage": True}

        start = time.time()
        with self._open("POST", "/chat/completions", body) as resp:
            if self.stream:
                res = self._read_stream(resp, start)
            else:
                res = self._read_json(resp)
        res["elapsed"] = time.time() - start
        res["server"] = self.server
        return res

    def _read_json(self, resp):
        out = json.load(resp)
        if not out.get("choices"):
            raise RuntimeError("응답에 choices 없음")
        c = out["choices"][0]
        msg = c.get("message") or {}
        reasoning, content = split_think(msg.get("content") or "")
        # sglang·구 vLLM 은 reasoning_content, Ollama·신 vLLM 은 reasoning
        rc = (msg.get("reasoning_content") or "") + (msg.get("reasoning") or "")
        if rc:
            reasoning = rc.strip()
        if reasoning and not self.hide_think:
            sys.stderr.write("<think>\n%s\n</think>\n" % reasoning.strip())
            sys.stderr.flush()
        print(content, flush=True)
        return {"content": content, "reasoning": reasoning, "finish": c.get("finish_reason"),
                "usage": out.get("usage"), "ttft": None}

    def _read_stream(self, resp, start):
        res = {"usage": None, "finish": None, "ttft": None}
        content, reasoning = [], []
        in_think = False
        for raw in resp:
            line = raw.decode("utf-8", "replace").strip()
            if not line.startswith("data:"):
                continue
            data = line[len("data:"):].strip()
            if data == "[DONE]":
                break
            try:
                chunk = json.loads(data)
            except ValueError:
                continue
            if chunk.get("usage"):
                res["usage"] = chunk["usage"]
            for c in chunk.get("choices") or []:
                d = c.get("delta") or {}
                rc = (d.get("reasoning_content") or "") + (d.get("reasoning") or "")
                cc = d.get("content") or ""
                if res["ttft"] is None and (rc or cc):
                    res["ttft"] = time.time() - start
                if rc:
                    if not in_think and not self.hide_think:
                        sys.stderr.write("<think>\n")
                    in_think = True
                    reasoning.append(rc)
                    if not self.hide_think:
                        sys.stderr.write(rc)
                        sys.stderr.flush()
                if cc:
                    if in_think and not self.hide_think:
                        sys.stderr.write("\n</think>\n")
                        sys.stderr.flush()
                    in_think = False
                    content.append(cc)
                    sys.stdout.write(cc)
                    sys.stdout.flush()
                if c.get("finish_reason"):
                    res["finish"] = c["finish_reason"]
        if in_think and not self.hide_think:
            sys.stderr.write("\n</think>\n")
        sys.stdout.write("\n")
        sys.stdout.flush()
        tagged, answer = split_think("".join(content))
        res["content"] = answer
        res["reasoning"] = "".join(reasoning) + tagged
        return res


def print_stats(r):
    parts = ["server=%s" % r["server"], "소요 %.3fs" % r["elapsed"]]
    if r.get("ttft"):
        parts.append("첫토큰 %.3fs" % r["ttft"])
    parts.append("추론 %d자" % len(r["reasoning"]))
    if not r["content"].strip():
        parts.append("본문 없음")  # 모델이 추론 안에서 답을 끝내고 본문을 비우는 경우가 있다
    u = r.get("usage")
    if u:
        tok = "토큰 prompt=%s completion=%s" % (u.get("prompt_tokens"), u.get("completion_tokens"))
        details = u.get("completion_tokens_details") or {}
        if "reasoning_tokens" in details:
            tok += " reasoning=%s" % details["reasoning_tokens"]
        parts.append(tok)
    if r.get("finish") and r["finish"] != "stop":
        parts.append("finish=" + r["finish"])
    sys.stderr.write("[%s]\n" % " | ".join(parts))
    sys.stderr.flush()


def run_chat(cli, system, think):
    history = [{"role": "system", "content": system}]
    sys.stderr.write("멀티턴 대화 모드. 명령: /think on|off|auto, /effort none|low|medium|high (값 없으면 안 보냄), /reset, /history, /quit\n")
    while True:
        sys.stderr.write("\n[턴 %d | think=%s] > " % (len(history) // 2 + 1, think))
        sys.stderr.flush()
        line = sys.stdin.readline()
        if not line:
            return
        line = line.strip()
        if not line:
            continue
        if line.startswith("/"):
            cmd, _, arg = line.partition(" ")
            arg = arg.strip()
            if cmd in ("/quit", "/exit", "/q"):
                return
            elif cmd == "/reset":
                history = history[:1]
                sys.stderr.write("대화 기록 초기화\n")
            elif cmd == "/history":
                for m in history:
                    sys.stderr.write("%-9s %s\n" % (m["role"] + ":", m["content"]))
            elif cmd == "/think":
                try:
                    parse_think(arg)
                    think = arg
                except ValueError as e:
                    sys.stderr.write("%s\n" % e)
            elif cmd == "/effort":
                cli.effort = arg
                sys.stderr.write("reasoning_effort=%r\n" % cli.effort)
            else:
                sys.stderr.write("알 수 없는 명령: %s\n" % cmd)
            continue
        history.append({"role": "user", "content": line})
        try:
            r = cli.complete(history, think)
        except Exception as e:  # noqa: BLE001 - 대화는 계속한다
            history.pop()
            sys.stderr.write("ERROR: %s\n" % e)
            continue
        print_stats(r)
        # 추론 과정은 히스토리에 넣지 않는다 (DeepSeek·Qwen 권장 방식).
        history.append({"role": "assistant", "content": r["content"]})


def scenario_files(spec):
    files = []
    for p in (s.strip() for s in spec.split(",")):
        if not p:
            continue
        if os.path.isdir(p):
            files.extend(sorted(glob.glob(os.path.join(p, "*.json"))))
        elif os.path.isfile(p):
            files.append(p)
        else:
            raise FileNotFoundError(p)
    if not files:
        raise FileNotFoundError("시나리오 파일 없음: " + spec)
    return files


def check_expect(answer, expect):
    """답변에 기대 문자열이 모두 있는지 본다 (대소문자 무시, "a|b" 는 둘 중 하나)."""
    low = answer.lower()
    return [e for e in expect if not any(alt.strip().lower() in low for alt in e.split("|"))]


def run_scenarios(cli, spec, default_system, default_think):
    rows = []
    total_fails = 0
    for f in scenario_files(spec):
        with open(f, encoding="utf-8") as fp:
            sc = json.load(fp)
        name = sc.get("name") or os.path.splitext(os.path.basename(f))[0]
        system = sc["system"] if "system" in sc else default_system
        turns = sc.get("turns") or []
        history = [{"role": "system", "content": system}] if system else []
        row = {"name": name, "turns": len(turns), "fails": 0, "elapsed": 0.0, "note": ""}
        for i, t in enumerate(turns):
            think = t.get("think") or sc.get("think") or default_think
            print("\n=== [%s] 턴 %d/%d (think=%s) ===\nUSER: %s" % (name, i + 1, len(turns), think, t["user"]))
            sys.stdout.write("ASSISTANT: ")
            sys.stdout.flush()
            history.append({"role": "user", "content": t["user"]})
            try:
                r = cli.complete(history, think)
            except Exception as e:  # noqa: BLE001 - 나머지 시나리오는 계속 돌린다
                row["fails"] += len(turns) - i
                row["note"] = str(e)
                sys.stderr.write("ERROR: %s\n" % e)
                break
            print_stats(r)
            row["elapsed"] += r["elapsed"]
            history.append({"role": "assistant", "content": r["content"]})
            expect = t.get("expect") or []
            if expect:
                miss = check_expect(r["content"], expect)
                if miss:
                    row["fails"] += 1
                    print("CHECK: FAIL (없음: %s)" % ", ".join(miss))
                else:
                    print("CHECK: PASS (%s)" % ", ".join(expect))
        total_fails += row["fails"]
        rows.append(row)

    print("\n=== 결과 ===")
    for r in rows:
        status = "FAIL %d" % r["fails"] if r["fails"] else "PASS"
        line = "%-24s 턴 %2d  %-7s %.3fs" % (r["name"], r["turns"], status, r["elapsed"])
        if r["note"]:
            line += "  " + r["note"]
        print(line)
    return total_fails


def main():
    argv = sys.argv[1:]
    cfg_path = find_config()
    preset = ""
    for i, a in enumerate(argv):
        if a in ("-config", "--config") and i + 1 < len(argv):
            cfg_path = argv[i + 1]
        elif a.lstrip("-").startswith("config="):
            cfg_path = a.lstrip("-")[len("config="):]
        elif a in ("-preset", "--preset") and i + 1 < len(argv):
            preset = argv[i + 1]
        elif a.lstrip("-").startswith("preset="):
            preset = a.lstrip("-")[len("preset="):]
    cfg = {}
    if cfg_path:
        try:
            cfg = load_toml(cfg_path)
        except (OSError, ValueError, SyntaxError) as e:
            sys.exit("ERROR: 설정파일: %s" % e)

    if preset and not any(k.startswith("preset.%s." % preset) for k in cfg):
        sys.exit("ERROR: 프리셋 없음: %r (.env.toml 의 [preset.%s] 를 확인하세요)" % (preset, preset))

    # 우선순위: 플래그 > -preset > 환경변수 > [llm]
    def conf(env, key, default):
        return ((preset and cfg.get("preset.%s.%s" % (preset, key))) or os.environ.get(env)
                or cfg.get("llm." + key) or cfg.get(key) or default)

    ap = argparse.ArgumentParser(description="OpenAI 호환 LLM 호출 테스트")
    ap.add_argument("-config", default=cfg_path, help="설정파일 경로")
    ap.add_argument("-preset", default=preset, help=".env.toml 의 [preset.<id>] 로 접속 (예: -preset dev)")
    ap.add_argument("-url", default=conf("LLM_BASE_URL", "base_url", ""), help="API base URL")
    ap.add_argument("-model", default=conf("LLM_MODEL", "model", ""), help="모델명")
    ap.add_argument("-key", default=conf("LLM_API_KEY", "api_key", ""), help="API 키 (필요한 경우)")
    ap.add_argument("-p", default="안녕하세요. 간단히 자기소개 해주세요.", help="사용자 프롬프트")
    ap.add_argument("-sys", default=conf("LLM_SYSTEM", "system", "You are a helpful assistant. 한국어로 답변하세요."),
                    help="시스템 프롬프트")
    ap.add_argument("-t", type=float, default=float(conf("LLM_TEMPERATURE", "temperature", "0.7")), help="temperature")
    ap.add_argument("-max", type=int, default=int(conf("LLM_MAX_TOKENS", "max_tokens", "1024")), help="max_tokens")
    ap.add_argument("-stream", action="store_true", help="스트리밍 응답")
    ap.add_argument("-models", action="store_true", help="모델 목록만 조회")
    ap.add_argument("-timeout", type=float, default=300, help="요청 타임아웃(초)")
    ap.add_argument("-server", default=conf("LLM_SERVER", "server", "auto"),
                    help="서버 종류: auto(/v1/models 로 판별) / sglang / vllm / ollama / openai")
    ap.add_argument("-think", default=conf("LLM_THINK", "think", "auto"), help="thinking 모드: on / off / auto(서버 기본값)")
    ap.add_argument("-effort", default=conf("LLM_REASONING_EFFORT", "reasoning_effort", ""),
                    help="reasoning_effort: none / low / medium / high (비우면 안 보냄, Ollama 는 none 으로 thinking 끔)")
    ap.add_argument("-hide-think", dest="hide_think", action="store_true", help="추론 과정(reasoning) 출력 숨김")
    ap.add_argument("-chat", action="store_true", help="대화형 멀티턴 모드")
    ap.add_argument("-scenario", default="", help="멀티턴 시나리오 JSON 파일 또는 폴더 (쉼표로 여러 개)")
    args = ap.parse_args(argv)

    if not args.url:
        sys.exit("ERROR: 접속주소 없음. .env.toml 의 base_url 을 설정하세요 (.env.toml.example 참고)")
    if not args.model and not args.models:
        sys.exit("ERROR: 모델명 없음. .env.toml 의 model 을 설정하세요")
    try:
        parse_think(args.think)
        args.server = normalize_server(args.server)
    except ValueError as e:
        sys.exit("ERROR: %s" % e)

    cli = Client(args)
    if cli.server == "auto" and not args.models:
        cli.detect_server()
    try:
        if args.models:
            cli.models()
        elif args.scenario:
            if run_scenarios(cli, args.scenario, args.sys, args.think) > 0:
                sys.exit(2)
        elif args.chat:
            run_chat(cli, args.sys, args.think)
        else:
            msgs = [{"role": "system", "content": args.sys}, {"role": "user", "content": args.p}]
            print_stats(cli.complete(msgs, args.think))
    except (OSError, RuntimeError, ValueError) as e:
        sys.exit("ERROR: %s" % e)


if __name__ == "__main__":
    main()
