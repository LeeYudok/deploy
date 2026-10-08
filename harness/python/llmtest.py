#!/usr/bin/env python3
"""sglang(OpenAI 호환) LLM 호출 테스트 클라이언트 (Python, 표준 라이브러리만 사용)

사용법:
    python llmtest.py -p "안녕하세요"
    python llmtest.py -stream -think off -p "Python 장점 3가지"
    python llmtest.py -chat                     # 대화형 멀티턴
    python llmtest.py -scenario ../scenarios     # 시나리오 멀티턴 자동 검증
    python llmtest.py -bench 1,2,4,8             # 동시 처리 부하 테스트
    python llmtest.py -models

실행 기록은 ../runs/YYYY-MM-DD.jsonl 에 Go 클라이언트와 같은 형식으로 남긴다 (-no-record 로 끔).

접속 정보는 .env.toml 에서 읽는다 (../.env.toml.example 참고).
우선순위: 명령행 플래그 > 환경변수 > .env.toml > 기본값
Python 3.8 이상.
"""

import argparse
import ast
import glob
import json
import os
import queue
import sys
import threading
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


def find_up(name, want_dir=False):
    """현재 폴더와 스크립트 폴더에서 시작해 상위 2단계까지 name 을 찾는다."""
    for start in (os.getcwd(), os.path.dirname(os.path.abspath(__file__))):
        d = start
        for _ in range(3):
            c = os.path.join(d, name)
            if (os.path.isdir(c) if want_dir else os.path.isfile(c)):
                return c
            d = os.path.dirname(d)
    return ""


def presets_from(cfg):
    """[preset.<id>] 섹션을 order 순(0 이면 뒤에 id 순)으로 돌려준다."""
    by_id = {}
    for k, v in cfg.items():
        if not k.startswith("preset."):
            continue
        pid, _, field = k[len("preset."):].rpartition(".")
        if not pid:
            continue
        p = by_id.setdefault(pid, {"id": pid, "order": 0, "label": ""})
        if field == "order":
            try:
                p["order"] = int(v)
            except ValueError:
                p["order"] = 0
        elif field in ("label", "base_url", "model", "server", "think", "api_key"):
            p[field] = v
    out = list(by_id.values())
    for p in out:
        p["label"] = p["label"] or p["id"]
    out.sort(key=lambda p: (p["order"] or 1 << 30, p["id"]))
    return out


def prices_from(cfg):
    """[price."<모델>"] 섹션(input, output: 100만 토큰당 USD)을 읽는다."""
    out = {}
    for k, v in cfg.items():
        if not k.startswith("price."):
            continue
        model, _, field = k[len("price."):].rpartition(".")
        if not model:
            continue
        if len(model) >= 2 and model.startswith('"') and model.endswith('"'):
            try:
                model = ast.literal_eval(model)
            except (ValueError, SyntaxError):
                pass
        if field == "source":
            out.setdefault(model, {"input": 0.0, "output": 0.0, "source": ""})["source"] = v
            continue
        if field not in ("input", "output"):
            continue
        try:
            f = float(v)
        except ValueError:
            continue
        if f < 0:
            continue
        out.setdefault(model, {"input": 0.0, "output": 0.0, "source": ""})[field] = f
    return out


def price_cost(price, usage):
    """usage 의 환산 비용(USD). 추론 토큰은 completion 에 들어 있으므로 출력 단가로 센다."""
    if not price or not usage:
        return None
    if price["input"] <= 0 and price["output"] <= 0:
        return None
    return ((usage.get("prompt_tokens") or 0) / 1e6 * price["input"]
            + (usage.get("completion_tokens") or 0) / 1e6 * price["output"])


def fmt_usd(v):
    if v == 0:
        return "$0"
    if v < 0.01:
        return "$%.5f" % v
    if v < 1:
        return "$%.4f" % v
    return "$%.2f" % v


def fmt_ms(ms):
    """밀리초를 Go time.Duration 표기(150ms, 1.234s, 1m2.5s)로 쓴다."""
    if ms == 0:
        return "0s"
    if ms < 1000:
        return "%dms" % ms
    m, rest = divmod(ms, 60000)
    sec = ("%.3f" % (rest / 1000.0)).rstrip("0").rstrip(".")
    return ("%dm" % m if m else "") + sec + "s"


def one_line(s, limit):
    s = " ".join(s.split())
    if len(s) <= limit:
        return s
    return s[:limit] + "\n... (%d자 중 %d자만 표시)" % (len(s), limit)


# ---- 실행 기록 (runs/YYYY-MM-DD.jsonl, Go 클라이언트 runs.go 와 같은 형식) ----

SLOW_WRITE = 0.05  # 초. 쓰기가 이보다 느리면 경고한다


def now_str():
    t = time.time()
    return time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(t)) + ".%03d" % int(t % 1 * 1000)


def runs_dir(flag):
    """기록 폴더: -runs, scenarios 폴더 옆(harness/runs), 현재 폴더의 runs 순."""
    if flag:
        return flag
    d = find_up("scenarios", True)
    if d:
        return os.path.join(os.path.dirname(d), "runs")
    return "runs"


class RunStore:
    def __init__(self, d):
        self.dir = d
        self.lock = threading.Lock()

    def append(self, rec):
        line = json.dumps(rec, ensure_ascii=False, separators=(",", ":"))
        start = time.time()
        with self.lock:
            os.makedirs(self.dir, 0o700, exist_ok=True)
            path = os.path.join(self.dir, time.strftime("%Y-%m-%d") + ".jsonl")
            fd = os.open(path, os.O_CREAT | os.O_WRONLY | os.O_APPEND, 0o600)
            with os.fdopen(fd, "a", encoding="utf-8", newline="\n") as f:
                f.write(line + "\n")
        d = time.time() - start
        if d > SLOW_WRITE:
            sys.stderr.write("[slow] 기록 쓰기 느림: %s, %dms (기준 %dms)\n"
                             % (os.path.basename(path), d * 1000, SLOW_WRITE * 1000))


def usage_record(u):
    """usage 를 Go Usage 구조체와 같은 필드로 줄인다."""
    if not u:
        return None
    out = {"prompt_tokens": u.get("prompt_tokens") or 0, "completion_tokens": u.get("completion_tokens") or 0,
           "total_tokens": u.get("total_tokens") or 0}
    details = u.get("completion_tokens_details")
    if details:
        out["completion_tokens_details"] = {"reasoning_tokens": details.get("reasoning_tokens") or 0}
    return out


def drop_empty(rec, keys):
    """Go 의 omitempty 처럼 빈 값 필드를 뺀다."""
    for k in keys:
        if not rec.get(k):
            rec.pop(k, None)
    return rec


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
        self.runs = None  # RunStore. -no-record 면 None
        self.session = ""
        self.price = None  # 이 모델의 단가 ([price."<모델>"]). 없으면 비용을 안 보인다

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

    def complete(self, messages, think, quiet=False, stream=None):
        """messages 로 한 번 호출하고 답변을 출력한다. 추론 과정은 stderr 로 출력한다.

        quiet 면 아무것도 출력하지 않는다 (부하 테스트). stream 을 주면 -stream 대신 그 값을 쓴다.
        """
        if stream is None:
            stream = self.stream
        body = {
            "model": self.model,
            "messages": messages,
            "temperature": self.temp,
            "max_tokens": self.max_tokens,
            "stream": stream,
        }
        kw, effort = think_params(self.server, think, self.effort)
        if kw:
            body["chat_template_kwargs"] = kw
        if effort:
            body["reasoning_effort"] = effort
        if stream:
            body["stream_options"] = {"include_usage": True}

        start = time.time()
        with self._open("POST", "/chat/completions", body) as resp:
            if stream:
                res = self._read_stream(resp, start, quiet)
            else:
                res = self._read_json(resp, quiet)
        res["elapsed"] = time.time() - start
        res["server"] = self.server
        return res

    def call(self, messages, think, scenario="", turn=0):
        """complete 를 부르고 결과를 실행 기록에 남긴다. 호출 오류도 남기고 다시 던진다."""
        start = time.time()
        try:
            res = self.complete(messages, think)
        except Exception as e:
            self.record_turn(think, messages, {"elapsed": time.time() - start}, e, scenario, turn)
            raise
        self.record_turn(think, messages, res, None, scenario, turn)
        return res

    def record_turn(self, think, messages, res, err, scenario, turn):
        if self.runs is None:
            return
        rec = {
            "type": "turn", "time": now_str(), "source": "cli", "session": self.session,
            "scenario": scenario, "turn": turn, "base_url": self.base, "model": self.model,
            "server": self.server, "think": think or "auto", "reasoning_effort": self.effort,
            "stream": bool(self.stream), "temperature": self.temp, "max_tokens": self.max_tokens,
            "user": messages[-1]["content"] if messages else "",
            "content": res.get("content") or "", "reasoning": res.get("reasoning") or "",
            "usage": usage_record(res.get("usage")), "ttft_ms": int((res.get("ttft") or 0) * 1000),
            "elapsed_ms": int((res.get("elapsed") or 0) * 1000), "finish": res.get("finish") or "",
            "error": str(err) if err else "",
        }
        drop_empty(rec, ("session", "scenario", "turn", "reasoning_effort", "reasoning", "usage", "ttft_ms",
                         "finish", "error"))
        self.write_record(rec)

    def write_record(self, rec):
        if self.runs is None:
            return
        try:
            self.runs.append(rec)
        except OSError as e:
            sys.stderr.write("기록 쓰기 실패: %s\n" % e)

    def _read_json(self, resp, quiet=False):
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
        if reasoning and not quiet and not self.hide_think:
            sys.stderr.write("<think>\n%s\n</think>\n" % reasoning.strip())
            sys.stderr.flush()
        if not quiet:
            print(content, flush=True)
        return {"content": content, "reasoning": reasoning, "finish": c.get("finish_reason"),
                "usage": out.get("usage"), "ttft": None}

    def _read_stream(self, resp, start, quiet=False):
        res = {"usage": None, "finish": None, "ttft": None}
        content, reasoning = [], []
        in_think = False
        show_think = not quiet and not self.hide_think
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
                    if not in_think and show_think:
                        sys.stderr.write("<think>\n")
                    in_think = True
                    reasoning.append(rc)
                    if show_think:
                        sys.stderr.write(rc)
                        sys.stderr.flush()
                if cc:
                    if in_think and show_think:
                        sys.stderr.write("\n</think>\n")
                        sys.stderr.flush()
                    in_think = False
                    content.append(cc)
                    if not quiet:
                        sys.stdout.write(cc)
                        sys.stdout.flush()
                if c.get("finish_reason"):
                    res["finish"] = c["finish_reason"]
        if in_think and show_think:
            sys.stderr.write("\n</think>\n")
        if not quiet:
            sys.stdout.write("\n")
            sys.stdout.flush()
        tagged, answer = split_think("".join(content))
        res["content"] = answer
        res["reasoning"] = "".join(reasoning) + tagged
        return res


def print_stats(r, price=None):
    parts = ["server=%s" % r["server"], "소요 %.3fs" % r["elapsed"]]
    if r.get("ttft"):
        parts.append("첫토큰 %.3fs" % r["ttft"])
    parts.append("추론 %d자" % len(r["reasoning"]))
    cost = price_cost(price, r.get("usage"))
    if cost is not None:
        parts.append("환산 " + fmt_usd(cost))
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
            r = cli.call(history, think)
        except Exception as e:  # noqa: BLE001 - 대화는 계속한다
            history.pop()
            sys.stderr.write("ERROR: %s\n" % e)
            continue
        print_stats(r, cli.price)
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
        row = {"name": name, "turns": len(turns), "done": 0, "checks": 0, "fails": 0, "elapsed": 0.0, "note": "",
               "results": []}
        for i, t in enumerate(turns):
            think = t.get("think") or sc.get("think") or default_think
            print("\n=== [%s] 턴 %d/%d (think=%s) ===\nUSER: %s" % (name, i + 1, len(turns), think, t["user"]))
            sys.stdout.write("ASSISTANT: ")
            sys.stdout.flush()
            history.append({"role": "user", "content": t["user"]})
            try:
                r = cli.call(history, think, name, i + 1)
            except Exception as e:  # noqa: BLE001 - 나머지 시나리오는 계속 돌린다
                # 남은 검사를 실패로 센다. 검사가 남지 않았어도 호출 오류는 실패 1로 센다
                left = sum(1 for x in turns[i:] if x.get("expect")) or 1
                row["checks"] += left
                row["fails"] += left
                row["results"].append({"turn": i + 1, "expect": None, "missing": ["(호출 오류)"]})
                row["note"] = str(e)
                sys.stderr.write("ERROR: %s\n" % e)
                break
            print_stats(r, cli.price)
            row["elapsed"] += r["elapsed"]
            row["done"] += 1
            history.append({"role": "assistant", "content": r["content"]})
            expect = t.get("expect") or []
            if expect:
                miss = check_expect(r["content"], expect)
                row["checks"] += 1
                res = {"turn": i + 1, "expect": expect}
                if miss:
                    res["missing"] = miss
                row["results"].append(res)
                if miss:
                    row["fails"] += 1
                    print("CHECK: FAIL (없음: %s)" % ", ".join(miss))
                else:
                    print("CHECK: PASS (%s)" % ", ".join(expect))
        total_fails += row["fails"]
        rows.append(row)
        rec = {
            "type": "scenario", "time": now_str(), "source": "cli", "session": cli.session, "name": name,
            "base_url": cli.base, "model": cli.model, "server": cli.server,
            "think": "scenario" if sc.get("think") else default_think,
            "turns": row["turns"], "done": row["done"], "checks": row["checks"], "fails": row["fails"],
            "elapsed_ms": int(row["elapsed"] * 1000), "note": row["note"], "results": row["results"],
        }
        cli.write_record(drop_empty(rec, ("session", "note", "results")))

    print("\n=== 결과 ===")
    for r in rows:
        status = "FAIL %d" % r["fails"] if r["fails"] else "PASS"
        line = "%-24s 턴 %2d  %-7s %.3fs" % (r["name"], r["turns"], status, r["elapsed"])
        if r["note"]:
            line += "  " + r["note"]
        print(line)
    return total_fails


# ---- 동시 처리 부하 테스트 (Go bench.go 와 같은 방식) ----

MAX_BENCH_LEVEL = 128


def parse_levels(spec):
    """ "1,2,4,8" 을 읽는다."""
    out = []
    for p in (x.strip() for x in spec.split(",")):
        if not p:
            continue
        try:
            n = int(p)
        except ValueError:
            n = 0
        if n < 1 or n > MAX_BENCH_LEVEL:
            raise ValueError("동시 수준은 1~%d 의 정수: %r" % (MAX_BENCH_LEVEL, p))
        out.append(n)
    if not out:
        raise ValueError("동시 수준을 하나 이상 적으세요 (예: 1,2,4,8)")
    return out


def percentile(xs, p):
    if not xs:
        return 0
    s = sorted(xs)
    return s[int((len(s) - 1) * p + 0.5)]


def summarize_level(level, samples, wall):
    b = {"level": level, "requests": len(samples), "ok": 0, "errors": 0, "wall_ms": int(wall * 1000),
         "rps": 0.0, "tok_per_sec": 0.0, "req_tok_per_s": 0.0, "lat_p50_ms": 0, "lat_p95_ms": 0, "lat_max_ms": 0,
         "ttft_p50_ms": 0, "ttft_p95_ms": 0, "avg_completion_tokens": 0.0, "prompt_tokens": 0,
         "completion_tokens": 0, "first_error": ""}
    lat, ttft = [], []
    req_rate = 0.0
    for s in samples:
        if not s["ok"]:
            b["errors"] += 1
            b["first_error"] = b["first_error"] or s["error"]
            continue
        b["ok"] += 1
        lat.append(s["latency_ms"])
        if s["ttft_ms"] > 0:
            ttft.append(s["ttft_ms"])
        b["completion_tokens"] += s["completion_tokens"]
        b["prompt_tokens"] += s["prompt_tokens"]
        # 요청 하나의 출력 속도: 첫 토큰 이후 시간으로 나눈다
        gen = s["latency_ms"] - s["ttft_ms"]
        if gen > 0 and s["completion_tokens"] > 0:
            req_rate += s["completion_tokens"] / (gen / 1000.0)
    if wall > 0:
        b["rps"] = b["ok"] / wall
        b["tok_per_sec"] = b["completion_tokens"] / wall
    if b["ok"]:
        b["avg_completion_tokens"] = b["completion_tokens"] / float(b["ok"])
        b["req_tok_per_s"] = req_rate / b["ok"]
    b["lat_p50_ms"], b["lat_p95_ms"], b["lat_max_ms"] = percentile(lat, .5), percentile(lat, .95), percentile(lat, 1)
    b["ttft_p50_ms"], b["ttft_p95_ms"] = percentile(ttft, .5), percentile(ttft, .95)
    return drop_empty(b, ("first_error",))


def recommend_level(levels):
    """오류가 없고, 처리량이 이전 수준보다 10% 이상 늘어난 마지막 수준. 고를 수 없으면 0."""
    best, prev = 0, 0.0
    for i, b in enumerate(levels):
        if b["errors"] > 0 or b["ok"] == 0:
            break
        if i == 0 or b["tok_per_sec"] >= prev * 1.1:
            best, prev = b["level"], b["tok_per_sec"]
            continue
        break
    return best


def bench_level(cli, level, n, prompt, system, think, stop):
    """동시 level 개 스레드로 요청 n 건을 보낸다. 요청은 스트리밍으로 보내 TTFT 를 잰다."""
    jobs = queue.Queue()
    for i in range(n):
        jobs.put(i)
    samples = [None] * n

    def worker():
        while not stop.is_set():
            try:
                i = jobs.get_nowait()
            except queue.Empty:
                return
            msgs = [{"role": "system", "content": system}] if system else []
            # 프롬프트 끝에 번호를 붙여 응답 캐시 효과를 줄인다
            msgs.append({"role": "user", "content": "%s\n(요청 #%d-%d)" % (prompt, level, i + 1)})
            t0 = time.time()
            s = {"level": level, "index": i + 1, "ok": True, "ttft_ms": 0, "latency_ms": 0,
                 "completion_tokens": 0, "prompt_tokens": 0, "error": ""}
            try:
                r = cli.complete(msgs, think, quiet=True, stream=True)
                s["ttft_ms"] = int((r.get("ttft") or 0) * 1000)
                s["latency_ms"] = int(r["elapsed"] * 1000)
                u = r.get("usage") or {}
                s["completion_tokens"] = u.get("completion_tokens") or 0
                s["prompt_tokens"] = u.get("prompt_tokens") or 0
            except Exception as e:  # noqa: BLE001 - 오류도 표본으로 센다
                s["ok"] = False
                s["error"] = one_line(str(e), 300)
                s["latency_ms"] = int((time.time() - t0) * 1000)
            samples[i] = s

    start = time.time()
    threads = [threading.Thread(target=worker, daemon=True) for _ in range(level)]
    for t in threads:
        t.start()
    for t in threads:
        while t.is_alive():
            t.join(0.2)  # Windows 에서도 Ctrl+C 를 받도록 짧게 기다린다
    return summarize_level(level, [x for x in samples if x], time.time() - start)


def run_bench(cli, spec, requests, prompt, system, think):
    """부하 테스트를 돌리고 수준별 표를 출력한다. 중지했으면 True."""
    levels = parse_levels(spec)
    sys.stderr.write("부하 테스트: 동시 %s, 수준당 요청 %s, max_tokens %d, think=%s\n"
                     % (levels, "수준의 2배" if requests <= 0 else requests, cli.max_tokens, think))
    print("%6s %6s %5s %7s %9s %10s %9s %9s %9s %9s"
          % ("동시", "요청", "오류", "req/s", "tok/s", "요청당tok/s", "p50", "p95", "TTFT p50", "TTFT p95"))
    stop = threading.Event()
    res = []
    stopped = False
    try:
        for level in levels:
            n = requests if requests > 0 else level * 2
            n = max(n, level)  # 모든 작업자가 한 번은 일하도록
            b = bench_level(cli, level, n, prompt, system, think, stop)
            res.append(b)
            print("%6d %6d %5d %7.2f %9.1f %10.1f %9s %9s %9s %9s"
                  % (b["level"], b["requests"], b["errors"], b["rps"], b["tok_per_sec"], b["req_tok_per_s"],
                     fmt_ms(b["lat_p50_ms"]), fmt_ms(b["lat_p95_ms"]), fmt_ms(b["ttft_p50_ms"]),
                     fmt_ms(b["ttft_p95_ms"])), flush=True)
            if b.get("first_error"):
                print("       첫 오류: %s" % b["first_error"])
    except KeyboardInterrupt:
        stop.set()
        stopped = True
        sys.stderr.write("\n중지\n")
    rec = recommend_level(res)
    if rec > 0:
        print("\n권장 동시 수: %d (오류 없이 처리량이 10%% 이상 늘어난 마지막 수준)" % rec)
    else:
        print("\n권장 동시 수: 판단할 수 없음 (첫 수준부터 오류)")
    r = {"type": "bench", "time": now_str(), "source": "cli", "base_url": cli.base, "model": cli.model,
         "server": cli.server, "think": think, "max_tokens": cli.max_tokens, "prompt": prompt,
         "requests": requests, "levels": res, "recommend": rec, "stopped": stopped}
    cli.write_record(drop_empty(r, ("stopped",)))
    return stopped


def main():
    argv = sys.argv[1:]
    cfg_path = find_up(".env.toml")
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

    presets = presets_from(cfg)
    if preset and not any(p["id"] == preset for p in presets):
        have = ", ".join("%s(%s)" % (p["id"], p["label"]) for p in presets) or "없음"
        sys.exit("ERROR: 프리셋 없음: %r (.env.toml 의 [preset.%s] 를 확인하세요. 있는 프리셋: %s)"
                 % (preset, preset, have))

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
    ap.add_argument("-no-record", dest="no_record", action="store_true", help="실행 기록(runs/*.jsonl)을 남기지 않음")
    ap.add_argument("-runs", default="", help="실행 기록 폴더 (기본: scenarios 폴더 옆의 runs)")
    ap.add_argument("-bench", default="", help="동시 처리 부하 테스트: 동시 수준 목록 (예: 1,2,4,8)")
    ap.add_argument("-bench-requests", dest="bench_requests", type=int, default=0,
                    help="부하 테스트 수준당 요청 수 (0 이면 수준의 2배)")
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
    cli.price = prices_from(cfg).get(args.model)
    if not args.no_record and not args.models:
        cli.runs = RunStore(runs_dir(args.runs))
        cli.session = os.urandom(4).hex()
    if cli.server == "auto" and not args.models:
        cli.detect_server()
    try:
        if args.bench:
            if run_bench(cli, args.bench, args.bench_requests, args.p, args.sys, args.think):
                sys.exit(130)
        elif args.models:
            cli.models()
        elif args.scenario:
            if run_scenarios(cli, args.scenario, args.sys, args.think) > 0:
                sys.exit(2)
        elif args.chat:
            run_chat(cli, args.sys, args.think)
        else:
            msgs = [{"role": "system", "content": args.sys}, {"role": "user", "content": args.p}]
            print_stats(cli.call(msgs, args.think), cli.price)
    except (OSError, RuntimeError, ValueError) as e:
        sys.exit("ERROR: %s" % e)
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    main()
