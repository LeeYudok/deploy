"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const settingsForm = $("#settings");

function el(tag, attrs = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") n.className = v;
    else if (k === "text") n.textContent = v;
    else n.setAttribute(k, v);
  }
  for (const c of children) if (c != null) n.append(c);
  return n;
}

async function api(path, body) {
  const opt = body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
  const res = await fetch(path, opt);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

// ---- 설정 ----

function formValues() {
  const f = settingsForm.elements;
  return {
    base_url: f.base_url.value.trim(),
    api_key: f.api_key.value,
    model: f.model.value.trim(),
    server: f.server.value,
    think: f.think.value,
    reasoning_effort: f.reasoning_effort.value,
    temperature: Number(f.temperature.value || 0),
    max_tokens: Number(f.max_tokens.value || 0),
    stream: f.stream.checked,
    system: f.system.value,
  };
}

function setStatus(node, text, cls) {
  node.textContent = text;
  node.className = "small " + (cls || "");
}

async function loadConfig() {
  const c = await api("/api/config");
  const f = settingsForm.elements;
  f.base_url.value = c.base_url || "";
  f.model.value = c.model || "";
  f.server.value = c.server || "auto";
  f.think.value = ["on", "off"].includes((c.think || "").toLowerCase()) ? c.think.toLowerCase() : "auto";
  f.reasoning_effort.value = c.reasoning_effort || "";
  f.temperature.value = c.temperature;
  f.max_tokens.value = c.max_tokens;
  f.system.value = c.system || "";
  $("#keyRow").hidden = !c.api_key_set;
  $("#cfgPath").textContent = "설정파일 " + c.config_path;
}

settingsForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const status = $("#saveStatus");
  const v = formValues();
  v.clear_api_key = settingsForm.elements.clear_api_key.checked;
  try {
    const r = await api("/api/config", v);
    settingsForm.elements.api_key.value = "";
    settingsForm.elements.clear_api_key.checked = false;
    $("#keyRow").hidden = !r.api_key_set;
    setStatus(status, "저장됨", "ok");
  } catch (err) {
    setStatus(status, err.message, "err");
  }
});

settingsForm.addEventListener("input", () => setStatus($("#saveStatus"), "저장 안 됨", "muted"));

$("#loadModels").addEventListener("click", async () => {
  const v = formValues();
  const status = $("#saveStatus");
  try {
    const list = await api("/api/models", { base_url: v.base_url, api_key: v.api_key });
    const dl = $("#modelList");
    dl.replaceChildren(...list.map((m) => el("option", { value: m.id })));
    if (list.length && !v.model) settingsForm.elements.model.value = list[0].id;
    setStatus(status, `모델 ${list.length}개: ${list.map((m) => m.id).join(", ")}`, "ok");
  } catch (err) {
    setStatus(status, "모델 조회 실패: " + err.message, "err");
  }
});

// ---- 한 턴 호출 (SSE) ----

// callTurn 은 messages 로 한 번 호출한다. 화면 요소 view 에 추론·답변을 흘려 넣고 결과를 돌려준다.
async function callTurn(messages, think, view, signal) {
  const v = formValues();
  const res = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      base_url: v.base_url, api_key: v.api_key, model: v.model,
      temperature: v.temperature, max_tokens: v.max_tokens,
      reasoning_effort: v.reasoning_effort, stream: v.stream, server: v.server,
      think, messages,
    }),
    signal,
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  let done = null;
  for (;;) {
    const { value, done: eof } = await reader.read();
    if (eof) break;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = "message";
      let data = "";
      for (const line of block.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data += line.slice(5).trim();
      }
      const payload = data ? JSON.parse(data) : null;
      if (event === "reasoning") view.reasoning(payload);
      else if (event === "content") view.content(payload);
      else if (event === "done") done = payload;
      else if (event === "error") throw new Error(payload);
    }
  }
  if (!done) throw new Error("응답이 중간에 끊겼습니다");
  view.finish(done);
  return done;
}

function statsText(r, think) {
  const parts = [`server=${r.server}`, `think=${think}`, `소요 ${(r.elapsed_ms / 1000).toFixed(2)}s`];
  if (r.ttft_ms) parts.push(`첫토큰 ${(r.ttft_ms / 1000).toFixed(2)}s`);
  parts.push(`추론 ${[...(r.reasoning || "")].length}자`);
  const u = r.usage;
  if (u) {
    let t = `토큰 prompt=${u.prompt_tokens} completion=${u.completion_tokens}`;
    if (u.completion_tokens_details) t += ` reasoning=${u.completion_tokens_details.reasoning_tokens}`;
    parts.push(t);
  }
  if (r.finish && r.finish !== "stop") parts.push(`finish=${r.finish}`);
  return parts.join(" | ");
}

// assistantView 는 답변 상자를 만들고 스트리밍 조각을 받아 그린다.
function assistantView(container, think) {
  const box = el("div", { class: "msg assistant" }, el("span", { class: "label", text: "assistant" }));
  const thinkBody = el("div", { class: "body" });
  const details = el("details", { class: "think", open: "" }, el("summary", { text: "추론 과정" }), thinkBody);
  details.hidden = true;
  const answer = el("div");
  const stats = el("div", { class: "stats", text: "응답 대기 중..." });
  box.append(details, answer, stats);
  container.append(box);
  return {
    box,
    reasoning(t) { details.hidden = false; thinkBody.textContent += t; },
    content(t) { answer.textContent += t; },
    finish(r) {
      // 서버가 <think> 를 답변에 섞어 보낸 경우도 정리된 값으로 다시 그린다.
      answer.textContent = r.content;
      thinkBody.textContent = r.reasoning || "";
      details.hidden = !r.reasoning;
      details.open = false;
      stats.textContent = statsText(r, think);
    },
    fail(msg) { box.classList.add("error"); stats.textContent = "ERROR: " + msg; },
  };
}

// ---- 대화 탭 ----

const chatLog = $("#chatLog");
const composer = $("#composer");
let history = [];
let chatAbort = null;

function scrollChat() { chatLog.scrollTop = chatLog.scrollHeight; }

composer.elements.q.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    composer.requestSubmit();
  }
});

composer.addEventListener("submit", async (e) => {
  e.preventDefault();
  const q = composer.elements.q.value.trim();
  if (!q || chatAbort) return;
  const v = formValues();
  $(".empty", chatLog)?.remove();
  composer.elements.q.value = "";
  chatLog.append(el("div", { class: "msg user" }, el("span", { class: "label", text: `user · 턴 ${history.length / 2 + 1}` }), q));
  const view = assistantView(chatLog, v.think);
  scrollChat();

  const messages = [];
  if (v.system) messages.push({ role: "system", content: v.system });
  messages.push(...history, { role: "user", content: q });

  chatAbort = new AbortController();
  $("#send").disabled = true;
  $("#stopChat").disabled = false;
  try {
    const r = await callTurn(messages, v.think, {
      reasoning: (t) => { view.reasoning(t); scrollChat(); },
      content: (t) => { view.content(t); scrollChat(); },
      finish: view.finish,
    }, chatAbort.signal);
    // 추론 과정은 히스토리에 넣지 않는다.
    history.push({ role: "user", content: q }, { role: "assistant", content: r.content });
  } catch (err) {
    view.fail(err.name === "AbortError" ? "중지함 (이 턴은 대화 기록에 넣지 않음)" : err.message);
  } finally {
    chatAbort = null;
    $("#send").disabled = false;
    $("#stopChat").disabled = true;
    scrollChat();
  }
});

$("#stopChat").addEventListener("click", () => chatAbort?.abort());
$("#resetChat").addEventListener("click", () => {
  chatAbort?.abort();
  history = [];
  chatLog.replaceChildren(el("p", { class: "muted empty", text: "새 대화를 시작합니다." }));
});

// ---- 시나리오 탭 ----

let scenarios = [];
let scAbort = null;

async function loadScenarios() {
  const data = await api("/api/scenarios");
  scenarios = data.items;
  $("#scSource").textContent = data.source ? "폴더 " + data.source : "";
  const list = $("#scList");
  list.replaceChildren(...scenarios.map((s, i) => el("label", {},
    el("input", { type: "checkbox", value: String(i), checked: "" }),
    el("span", {},
      el("strong", { text: s.name }),
      el("span", { class: "muted small", text: `${s.file} · ${s.turns.length}턴${s.think ? " · think=" + s.think : ""}` })),
  )));
  for (const e of data.errors) list.append(el("p", { class: "err small", text: e }));
}

$("#scAll").addEventListener("click", () => {
  const boxes = [...document.querySelectorAll("#scList input")];
  const all = boxes.every((b) => b.checked);
  boxes.forEach((b) => { b.checked = !all; });
});

// checkExpect 는 답변에 기대 문자열이 모두 있는지 본다 (대소문자 무시, "a|b" 는 둘 중 하나). Go 쪽과 같은 규칙.
function checkExpect(answer, expect) {
  const low = answer.toLowerCase();
  return (expect || []).filter((e) => !e.split("|").some((alt) => low.includes(alt.trim().toLowerCase())));
}

function renderSummary(rows) {
  const tbody = el("tbody", {}, ...rows.map((r) => el("tr", {},
    el("td", { text: r.name }),
    el("td", { class: "num", text: `${r.done}/${r.turns}` }),
    el("td", {}, r.running ? el("span", { class: "muted", text: "실행 중" })
      : el("span", { class: "badge " + (r.fails ? "fail" : "pass"), text: r.fails ? `FAIL ${r.fails}` : "PASS" })),
    el("td", { class: "num", text: (r.elapsed / 1000).toFixed(2) + "s" }),
    el("td", { class: "small err", text: r.note || "" }),
  )));
  $("#scSummary").replaceChildren(el("table", {},
    el("thead", {}, el("tr", {}, ...["시나리오", "턴", "결과", "소요", "비고"].map((h) => el("th", { text: h })))),
    tbody));
}

$("#scRun").addEventListener("click", async () => {
  const picked = [...document.querySelectorAll("#scList input:checked")].map((b) => scenarios[Number(b.value)]);
  if (!picked.length || scAbort) return;
  const v = formValues();
  const force = $("#scThink").value;
  const log = $("#scLog");
  log.replaceChildren();
  const rows = picked.map((s) => ({ name: s.name, turns: s.turns.length, done: 0, fails: 0, elapsed: 0, note: "", running: true }));
  renderSummary(rows);

  scAbort = new AbortController();
  $("#scRun").disabled = true;
  $("#scStop").disabled = false;
  try {
    for (const [si, s] of picked.entries()) {
      const row = rows[si];
      const group = el("div", { class: "scgroup" }, el("h3", { text: s.name }));
      log.append(group);
      const system = s.system ?? v.system;
      const messages = system ? [{ role: "system", content: system }] : [];
      for (const [ti, t] of s.turns.entries()) {
        const think = force || t.think || s.think || v.think;
        group.append(
          el("div", { class: "turnhead", text: `턴 ${ti + 1}/${s.turns.length}` }),
          el("div", { class: "msg user" }, t.user));
        const view = assistantView(group, think);
        messages.push({ role: "user", content: t.user });
        let r;
        try {
          r = await callTurn(messages, think, view, scAbort.signal);
        } catch (err) {
          view.fail(err.name === "AbortError" ? "중지함" : err.message);
          row.fails += s.turns.length - ti;
          row.note = err.name === "AbortError" ? "중지함" : err.message;
          if (err.name === "AbortError") throw err;
          break;
        }
        messages.push({ role: "assistant", content: r.content });
        row.done++;
        row.elapsed += r.elapsed_ms;
        if (t.expect?.length) {
          const miss = checkExpect(r.content, t.expect);
          if (miss.length) row.fails++;
          view.box.querySelector(".stats").prepend(
            el("span", { class: "badge " + (miss.length ? "fail" : "pass"),
              text: miss.length ? `FAIL 없음: ${miss.join(", ")}` : `PASS ${t.expect.join(", ")}` }), " ");
        }
        renderSummary(rows);
      }
      row.running = false;
      renderSummary(rows);
    }
  } catch (err) {
    if (err.name !== "AbortError") log.append(el("p", { class: "err", text: err.message }));
  } finally {
    rows.forEach((r) => { r.running = false; });
    renderSummary(rows);
    scAbort = null;
    $("#scRun").disabled = false;
    $("#scStop").disabled = true;
  }
});

$("#scStop").addEventListener("click", () => scAbort?.abort());

// ---- 탭 ----

for (const b of document.querySelectorAll(".tabs button")) {
  b.addEventListener("click", () => {
    for (const o of document.querySelectorAll(".tabs button")) {
      const on = o === b;
      o.setAttribute("aria-selected", String(on));
      $("#tab-" + o.dataset.tab).hidden = !on;
    }
  });
}

loadConfig().catch((err) => setStatus($("#saveStatus"), "설정 불러오기 실패: " + err.message, "err"));
loadScenarios().catch((err) => $("#scList").replaceChildren(el("p", { class: "err small", text: err.message })));
