// E2E 용 모의 LLM 서버 (OpenAI 호환, sglang 흉내). Node 표준 모듈만 쓴다.
//
//   node mock-llm.mjs          # MOCK_PORT (기본 18901)
//
// 답은 마지막 질문으로 정한다 (결과가 매번 같도록):
//   "ping" → pong · "내 이름은 X" → 기억 · "이름?" → 앞 턴의 X · "a*b" → 곱
//   "코드" → main.go 코드 블록 · "천천히" → 6초 동안 느리게 스트리밍 · "빈본문" → 추론만 주고 본문은 비움
// chat_template_kwargs.thinking 이 true 면 reasoning_content 를 먼저 보낸다 (sglang deepseek-v3 파서처럼).
// GET /__requests 는 최근 요청(본문과 Authorization 헤더 유무)을 돌려준다.
import http from "node:http";

const port = Number(process.env.MOCK_PORT || 18901);
const requests = [];

function answer(messages) {
  const users = messages.filter((m) => m.role === "user").map((m) => m.content);
  const last = users.at(-1) || "";
  if (/ping/i.test(last)) return "pong";
  const named = /내 이름은\s*([^\s,.!]+?)(?:이야|야|입니다)?[\s,.!]*$/.exec(last);
  if (named) return `기억했어요, ${named[1]} 님.`;
  if (/이름/.test(last)) {
    for (const u of users.slice(0, -1).reverse()) {
      const m = /내 이름은\s*([^\s,.!]+?)(?:이야|야|입니다)?[\s,.!]*$/.exec(u);
      if (m) return `${m[1]} 입니다.`;
    }
    return "모르겠어요.";
  }
  const mul = /(\d+)\s*[*x×]\s*(\d+)/.exec(last);
  if (mul) return String(Number(mul[1]) * Number(mul[2]));
  if (/코드/.test(last)) return 'main.go:\n```go\npackage main\n\nfunc main() {\n\tprintln("hi")\n}\n```\n실행은 `go run main.go` 입니다.';
  if (/천천히/.test(last)) return Array.from({ length: 60 }, (_, i) => `${i + 1}`).join(", ");
  if (/빈본문/.test(last)) return "";
  return "알겠습니다.";
}

function send(res, code, body) {
  res.writeHead(code, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/v1/models") {
    return send(res, 200, { object: "list", data: [{ id: "mock-model", object: "model", owned_by: "sglang", max_model_len: 8192 }] });
  }
  if (req.method === "GET" && req.url === "/__requests") return send(res, 200, requests.slice(-50));
  if (req.method !== "POST" || req.url !== "/v1/chat/completions") return send(res, 404, { error: "not found" });

  let raw = "";
  req.on("data", (c) => { raw += c; });
  req.on("end", async () => {
    const body = JSON.parse(raw);
    requests.push({ body, auth: req.headers.authorization || "" });
    const last = body.messages.at(-1)?.content || "";
    const thinking = body.chat_template_kwargs?.thinking === true;
    const content = answer(body.messages);
    const reasoning = thinking || /빈본문/.test(last) ? `생각: ${last} → ${content || "(답을 추론 안에서 끝냄)"}` : "";
    const usage = { prompt_tokens: raw.length >> 2, completion_tokens: content.length + reasoning.length, total_tokens: 0 };

    if (!body.stream) {
      const message = { role: "assistant", content };
      if (reasoning) message.reasoning_content = reasoning;
      return send(res, 200, { choices: [{ index: 0, message, finish_reason: "stop" }], usage });
    }
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    const chunk = (delta, extra = {}) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, ...extra }] })}\n\n`);
    const slow = /천천히/.test(last);
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    if (reasoning) chunk({ reasoning_content: reasoning });
    for (const piece of content.match(/[\s\S]{1,4}/g) || []) {
      if (res.destroyed) return;
      chunk({ content: piece });
      await sleep(slow ? 100 : 2);
    }
    chunk({}, { finish_reason: "stop" });
    res.write(`data: ${JSON.stringify({ choices: [], usage })}\n\n`);
    res.end("data: [DONE]\n\n");
  });
});

server.listen(port, "127.0.0.1", () => console.log(`mock LLM http://127.0.0.1:${port}/v1`));
