"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const settingsForm = $("#settings");

function el(tag, attrs = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === "class") n.className = v;
    else if (k === "text") n.textContent = v;
    else n.setAttribute(k, v === true ? "" : v);
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

function store(key, value) {
  try {
    if (value === undefined) return localStorage.getItem(key);
    if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value);
  } catch (e) { /* 저장소를 못 쓰는 브라우저면 무시 */ }
  return null;
}

// ---- 테마 (system → light → dark) ----

const themeBtn = $("#themeBtn");
function renderTheme() {
  const t = document.documentElement.dataset.theme || "system";
  const name = { system: "monitor", light: "sun", dark: "moon" }[t];
  themeBtn.replaceChildren(icon(name === "monitor" ? (matchMedia("(prefers-color-scheme: dark)").matches ? "moon" : "sun") : name));
  themeBtn.title = { system: "테마: 시스템", light: "테마: 라이트", dark: "테마: 다크" }[t];
}
themeBtn.addEventListener("click", () => {
  const order = ["system", "light", "dark"];
  const cur = document.documentElement.dataset.theme || "system";
  const next = order[(order.indexOf(cur) + 1) % order.length];
  if (next === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = next;
  store("theme", next === "system" ? null : next);
  renderTheme();
});
renderTheme();

// ---- 사이드바 (모바일 서랍) ----

function sidebar(open) {
  $("#sidebar").classList.toggle("open", open);
  $("#backdrop").hidden = !open;
}
$("#openSidebar").addEventListener("click", () => sidebar(true));
$("#closeSidebar").addEventListener("click", () => sidebar(false));
$("#backdrop").addEventListener("click", () => sidebar(false));

// ---- 설정 ----

function formValues() {
  const f = settingsForm.elements;
  return {
    base_url: f.base_url.value.trim(),
    api_key: f.api_key.value,
    model: f.model.value.trim(),
    preset: f.preset.value,
    server: f.server.value,
    think: f.think.value || "auto",
    reasoning_effort: f.reasoning_effort.value,
    temperature: Number(f.temperature.value || 0),
    max_tokens: Number(f.max_tokens.value || 0),
    stream: f.stream.checked,
    system: f.system.value,
  };
}

function saveStatus(text, cls = "") {
  const s = $("#saveStatus");
  s.textContent = text;
  s.className = cls;
}

function setStatus(state, server, model) {
  $("#statusDot").className = "dot " + (state || "");
  if (server !== undefined) $("#statusServer").textContent = server;
  if (model !== undefined) $("#statusModel").textContent = model || "-";
}

const SERVER_LABEL = { sglang: "sglang", vllm: "vLLM", ollama: "Ollama", openai: "OpenAI 호환" };

// ---- 프리셋 ----

let presets = [];
let savedKey = { base: "", set: false };

const sameBase = (a, b) => !!a && a.replace(/\/+$/, "") === (b || "").replace(/\/+$/, "");

// keyHint 는 지금 접속주소에 서버가 쓸 저장 키가 있는지 보여 준다 (키는 그 주소로만 보낸다).
function keyHint() {
  const f = settingsForm.elements;
  const p = presets.find((x) => x.id === f.preset.value);
  const presetKey = p && p.api_key_set && sameBase(p.base_url, f.base_url.value);
  const llmKey = savedKey.set && sameBase(savedKey.base, f.base_url.value);
  f.api_key.placeholder = presetKey ? "프리셋 키 사용" : llmKey ? "저장된 키 사용" : "없음";
  $("#keyRow").hidden = !llmKey || presetKey;
}

function renderPresets() {
  const sel = settingsForm.elements.preset;
  sel.replaceChildren(el("option", { value: "", text: "직접 입력" }),
    ...presets.map((p) => el("option", { value: p.id, text: `${p.label} · ${p.model}` })));
  const f = settingsForm.elements;
  const cur = presets.find((p) => sameBase(p.base_url, f.base_url.value) && p.model === f.model.value);
  sel.value = cur ? cur.id : "";
}

function applyPreset(id) {
  const p = presets.find((x) => x.id === id);
  if (!p) { keyHint(); return; }
  const f = settingsForm.elements;
  f.base_url.value = p.base_url;
  f.model.value = p.model;
  f.server.value = p.server || "auto";
  if (p.think) f.think.value = p.think;
  f.api_key.value = "";
  keyHint();
  refreshModels(true);
}

async function loadConfig() {
  const c = await api("/api/config");
  presets = c.presets || [];
  savedKey = { base: c.base_url, set: c.api_key_set };
  const f = settingsForm.elements;
  f.base_url.value = c.base_url || "";
  f.model.value = c.model || "";
  f.server.value = c.server || "auto";
  const think = (c.think || "").toLowerCase();
  f.think.value = ["on", "off"].includes(think) ? think : "auto";
  f.reasoning_effort.value = c.reasoning_effort || "";
  f.temperature.value = c.temperature;
  f.max_tokens.value = c.max_tokens;
  f.system.value = c.system || "";
  renderPresets();
  keyHint();
  $("#cfgPath").textContent = c.config_path;
  setStatus("", "서버 확인 전", c.model);
}

async function refreshModels(quiet) {
  const v = formValues();
  if (!v.base_url) return;
  setStatus("busy", "확인 중...", v.model);
  try {
    const r = await api("/api/models", { base_url: v.base_url, api_key: v.api_key, model: v.model, preset: v.preset });
    // 서버가 알려 준 모델에 프리셋 모델을 더해 콤보에 보여 준다.
    const ids = [...new Set([...r.models.map((m) => m.id), ...presets.map((p) => p.model)])];
    $("#modelList").replaceChildren(...ids.map((id) => el("option", { value: id })));
    if (r.models.length && !v.model) settingsForm.elements.model.value = r.models[0].id;
    const server = v.server === "auto" ? r.server : v.server;
    setStatus("ok", `${SERVER_LABEL[server] || server} 연결됨`, settingsForm.elements.model.value);
    if (!quiet) saveStatus(`모델 ${r.models.length}개 · owned_by=${r.owned_by || "-"}`, "ok");
  } catch (err) {
    setStatus("err", "연결 실패", v.model);
    if (!quiet) saveStatus(err.message, "err");
  }
}

settingsForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const v = formValues();
  v.clear_api_key = settingsForm.elements.clear_api_key.checked;
  try {
    const r = await api("/api/config", v);
    settingsForm.elements.api_key.value = "";
    settingsForm.elements.clear_api_key.checked = false;
    savedKey = { base: v.base_url, set: r.api_key_set };
    keyHint();
    saveStatus("저장했습니다", "ok");
  } catch (err) {
    saveStatus(err.message, "err");
  }
});
settingsForm.addEventListener("input", (e) => {
  if (e.target.name !== "think") saveStatus("저장하지 않은 변경이 있습니다");
});
settingsForm.elements.base_url.addEventListener("change", () => { renderPresets(); keyHint(); refreshModels(true); });
settingsForm.elements.model.addEventListener("change", renderPresets);
settingsForm.elements.preset.addEventListener("change", (e) => applyPreset(e.target.value));
// 대화창의 thinking 버튼은 settings 폼에 속해 있어서 Enter 를 누르면 저장이 돼 버린다. 막는다.
for (const r of $$("input[name=think]")) {
  r.addEventListener("keydown", (e) => { if (e.key === "Enter") e.preventDefault(); });
}
$("#loadModels").addEventListener("click", () => refreshModels(false));

// ---- 답변 표시 ----

// renderText 는 답변을 안전하게 그린다. ``` 코드 블록과 `인라인 코드`만 구분하고 나머지는 글자 그대로 둔다.
function renderText(container, text) {
  const nodes = [];
  const parts = text.split(/```/);
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      const body = part.replace(/^[^\n]*\n/, "");
      nodes.push(el("pre", {}, el("code", { text: body.replace(/\n$/, "") })));
      return;
    }
    for (const para of part.split(/\n{2,}/)) {
      if (!para.trim()) continue;
      const p = el("p");
      para.split(/(`[^`\n]+`)/).forEach((seg, j) => {
        p.append(j % 2 === 1 ? el("code", { text: seg.slice(1, -1) }) : seg);
      });
      nodes.push(p);
    }
  });
  container.replaceChildren(...nodes);
}

function fmtSec(ms) { return (ms / 1000).toFixed(ms < 10000 ? 2 : 1) + "s"; }

function statChips(r, think) {
  const chips = [
    el("span", { class: "chip accent" }, icon("cube"), SERVER_LABEL[r.server] || r.server || "?"),
    el("span", { class: "chip" }, icon("brain"), "think " + think),
    el("span", { class: "chip", title: "전체 소요" }, fmtSec(r.elapsed_ms)),
  ];
  if (r.ttft_ms) chips.push(el("span", { class: "chip", title: "첫 토큰까지" }, "TTFT " + fmtSec(r.ttft_ms)));
  const u = r.usage;
  if (u) {
    chips.push(el("span", { class: "chip", title: "prompt / completion 토큰" }, `${u.prompt_tokens} → ${u.completion_tokens} tok`));
    if (u.completion_tokens_details) chips.push(el("span", { class: "chip" }, `reasoning ${u.completion_tokens_details.reasoning_tokens} tok`));
  }
  if (r.finish && r.finish !== "stop") chips.push(el("span", { class: "chip fail" }, "finish=" + r.finish));
  return chips;
}

// assistantView 는 답변 칸을 만들고 스트리밍 조각을 받아 그린다.
function assistantView(container, think) {
  const thinkBody = el("div", { class: "think-body" });
  const label = el("span", { class: "label", text: "생각하는 중" });
  const details = el("details", { class: "think live", hidden: true },
    el("summary", {}, icon("brain"), label, icon("caret-down", "caret")), thinkBody);
  const answer = el("div", { class: "answer streaming" });
  const meta = el("div", { class: "meta" });
  const body = el("div", { class: "body" }, details, answer, meta);
  const box = el("div", { class: "msg assistant" }, el("div", { class: "avatar" }, icon("sparkle")), body);
  container.append(box);
  let raw = "";
  return {
    box,
    meta,
    reasoning(t) { details.hidden = false; thinkBody.textContent += t; },
    content(t) {
      if (!raw) label.textContent = "추론 과정";
      details.classList.remove("live");
      raw += t;
      answer.textContent = raw;
    },
    finish(r) {
      // 서버가 <think> 를 답변에 섞어 보낸 경우도 정리된 값으로 다시 그린다.
      answer.classList.remove("streaming");
      renderText(answer, r.content);
      details.classList.remove("live");
      thinkBody.textContent = r.reasoning || "";
      details.hidden = !r.reasoning;
      label.textContent = `추론 과정 · ${[...(r.reasoning || "")].length.toLocaleString()}자`;
      const copy = el("button", { type: "button", class: "icon-btn copy", title: "답변 복사", "aria-label": "답변 복사" }, icon("copy"));
      copy.addEventListener("click", async () => {
        try { await navigator.clipboard.writeText(r.content); copy.replaceChildren(icon("check")); } catch (e) { /* 클립보드 권한 없음 */ }
      });
      meta.replaceChildren(...statChips(r, think), copy);
    },
    fail(msg) {
      answer.classList.remove("streaming");
      details.classList.remove("live");
      body.append(el("div", { class: "error-box", text: msg }));
    },
  };
}

// ---- 한 턴 호출 (SSE) ----

async function callTurn(messages, think, view, signal) {
  const v = formValues();
  const res = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      base_url: v.base_url, api_key: v.api_key, model: v.model, server: v.server, preset: v.preset,
      temperature: v.temperature, max_tokens: v.max_tokens,
      reasoning_effort: v.reasoning_effort, stream: v.stream,
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
  const name = SERVER_LABEL[done.server] || done.server;
  setStatus("ok", `${name} 연결됨`, v.model);
  return done;
}

// ---- 대화 탭 ----

const chatLog = $("#chatLog");
const composer = $("#composer");
const q = composer.elements.q;
let history = [];
let chatAbort = null;

const SUGGESTIONS = [
  { title: "간단한 계산", sub: "thinking on/off 속도 비교", text: "17 × 23 은 얼마야? 숫자만 답해." },
  { title: "기억력 테스트", sub: "멀티턴으로 이어 묻기", text: "내 이름은 김민수고 좋아하는 숫자는 42야. 기억해 줘." },
  { title: "코드 생성", sub: "코드 블록 렌더링", text: "Go 로 문자열을 뒤집는 함수를 짧게 써 줘." },
  { title: "다국어", sub: "언어 전환 확인", text: "같은 인사말을 한국어, English, 日本語, 中文 으로 한 줄씩 써 줘." },
];

function renderEmpty() {
  const grid = el("div", { class: "suggest" }, ...SUGGESTIONS.map((s) => {
    const b = el("button", { type: "button" }, el("strong", { text: s.title }), el("small", { text: s.sub }));
    b.addEventListener("click", () => { q.value = s.text; autosize(); q.focus(); });
    return b;
  }));
  chatLog.replaceChildren(el("div", { class: "empty" },
    el("div", { class: "logo" }, el("span")),
    el("h2", { text: "무엇을 테스트할까요?" }),
    el("p", { text: "질문을 보내면 대화가 이어집니다. thinking 은 아래에서 턴마다 바꿀 수 있어요." }),
    grid));
  updateTurns();
}

function updateTurns() {
  const n = history.length / 2;
  $("#turnCount").textContent = n ? `${n}턴` : "";
}

function autosize() {
  q.style.height = "auto";
  q.style.height = Math.min(q.scrollHeight, 220) + "px";
}
q.addEventListener("input", autosize);

function scrollChat() { chatLog.scrollTop = chatLog.scrollHeight; }

q.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    composer.requestSubmit();
  }
});

function busy(on) {
  $("#send").hidden = on;
  $("#stopChat").hidden = !on;
}

composer.addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = q.value.trim();
  if (!text || chatAbort) return;
  const v = formValues();
  $(".empty", chatLog)?.remove();
  q.value = "";
  autosize();
  chatLog.append(el("div", { class: "msg user" },
    el("div", {}, el("div", { class: "bubble", text }), el("span", { class: "turn-tag", text: `턴 ${history.length / 2 + 1}` }))));
  const view = assistantView(chatLog, v.think);
  scrollChat();

  const messages = [];
  if (v.system) messages.push({ role: "system", content: v.system });
  messages.push(...history, { role: "user", content: text });

  chatAbort = new AbortController();
  busy(true);
  setStatus("busy");
  try {
    const r = await callTurn(messages, v.think, {
      reasoning: (t) => { view.reasoning(t); scrollChat(); },
      content: (t) => { view.content(t); scrollChat(); },
      finish: view.finish,
    }, chatAbort.signal);
    // 추론 과정은 히스토리에 넣지 않는다.
    history.push({ role: "user", content: text }, { role: "assistant", content: r.content });
    updateTurns();
  } catch (err) {
    const aborted = err.name === "AbortError";
    view.fail(aborted ? "중지했습니다. 이 턴은 대화 기록에 넣지 않습니다." : err.message);
    setStatus(aborted ? "ok" : "err");
  } finally {
    chatAbort = null;
    busy(false);
    scrollChat();
    q.focus();
  }
});

$("#stopChat").addEventListener("click", () => chatAbort?.abort());
$("#resetChat").addEventListener("click", () => {
  chatAbort?.abort();
  history = [];
  renderEmpty();
});

// ---- 시나리오 탭 ----

let scenarios = [];
let scAbort = null;

const LANG = [
  [/(^|-)ko(-|$)/, "한국어"], [/(^|-)en(-|$)/, "English"], [/(^|-)ja(-|$)/, "日本語"], [/(^|-)zh(-|$)/, "中文"],
];

async function loadScenarios() {
  const data = await api("/api/scenarios");
  scenarios = data.items;
  $("#scSource").textContent = data.source || "";
  const list = $("#scList");
  list.replaceChildren(...scenarios.map((s, i) => {
    const lang = LANG.find(([re]) => re.test(s.name))?.[1];
    return el("label", { class: "sc-card" },
      el("input", { type: "checkbox", value: String(i), checked: true }),
      el("span", { class: "inner" },
        el("strong", { text: s.name }),
        lang ? el("span", { class: "chip lang", text: lang }) : null,
        el("small", { text: `${s.turns.length}턴 · 검사 ${s.turns.filter((t) => t.expect?.length).length}개${s.think ? " · think=" + s.think : ""}` })),
      el("span", { class: "tick" }, icon("check")));
  }));
  for (const e of data.errors) list.append(el("p", { class: "error-box", text: e }));
}

$("#scAll").addEventListener("click", () => {
  const boxes = $$("#scList input");
  const all = boxes.every((b) => b.checked);
  boxes.forEach((b) => { b.checked = !all; });
});

// checkExpect 는 답변에 기대 문자열이 모두 있는지 본다 (대소문자 무시, "a|b" 는 둘 중 하나). Go 쪽과 같은 규칙.
function checkExpect(answer, expect) {
  const low = answer.toLowerCase();
  return (expect || []).filter((e) => !e.split("|").some((alt) => low.includes(alt.trim().toLowerCase())));
}

function renderSummary(rows) {
  const checks = rows.reduce((a, r) => a + r.checks, 0);
  const passed = rows.reduce((a, r) => a + r.checks - r.fails, 0);
  const elapsed = rows.reduce((a, r) => a + r.elapsed, 0);
  const turns = rows.reduce((a, r) => a + r.done, 0);
  const okRows = rows.filter((r) => !r.running && !r.fails).length;
  const rate = checks ? Math.round((passed / checks) * 100) : 0;
  const tile = (label, value, cls) => el("div", { class: "tile" }, el("small", { text: label }), el("b", { class: cls, text: value }));
  const tiles = el("div", { class: "tiles" },
    tile("검사 통과율", checks ? `${rate}%` : "-", checks ? (passed === checks ? "pass" : "fail") : ""),
    tile("통과한 시나리오", `${okRows} / ${rows.length}`),
    tile("총 소요", fmtSec(elapsed)),
    tile("턴 평균", turns ? fmtSec(elapsed / turns) : "-"));
  const board = el("div", { class: "board" },
    el("div", { class: "board-row head" }, el("span", { text: "시나리오" }), el("span", { class: "bar-cell", text: "진행" }), el("span", { text: "결과" }), el("span", { class: "num", text: "소요" })),
    ...rows.map((r) => {
      const pct = r.turns ? (r.done / r.turns) * 100 : 0;
      const state = r.running ? "" : r.fails ? "fail" : "pass";
      return el("div", { class: "board-row" },
        el("span", { text: r.name }),
        el("span", { class: "bar-cell" }, el("div", { class: "bar " + state }, el("span", { style: `width:${pct}%` }))),
        el("span", {}, r.running
          ? el("span", { class: "chip", text: `${r.done}/${r.turns}` })
          : el("span", { class: "chip " + state }, icon(r.fails ? "x-circle" : "check-circle"), r.fails ? `FAIL ${r.fails}` : "PASS")),
        el("span", { class: "num", text: fmtSec(r.elapsed) }),
        r.note ? el("span", { class: "note", text: r.note }) : null);
    }));
  $("#scSummary").replaceChildren(tiles, board);
}

$("#scRun").addEventListener("click", async () => {
  const picked = $$("#scList input:checked").map((b) => scenarios[Number(b.value)]);
  if (!picked.length || scAbort) return;
  const v = formValues();
  const force = $("input[name=scThink]:checked").value;
  const log = $("#scLog");
  log.replaceChildren();
  const rows = picked.map((s) => ({
    name: s.name, turns: s.turns.length, checks: s.turns.filter((t) => t.expect?.length).length,
    done: 0, fails: 0, elapsed: 0, note: "", running: true,
  }));
  renderSummary(rows);

  scAbort = new AbortController();
  $("#scRun").hidden = true;
  $("#scStop").hidden = false;
  setStatus("busy");
  try {
    for (const [si, s] of picked.entries()) {
      const row = rows[si];
      const turnsBox = el("div", { class: "sc-turns" });
      const head = el("summary", {}, icon("list-checks"), s.name, icon("caret-down", "caret"));
      const group = el("details", { class: "sc-group", open: true }, head, turnsBox);
      log.append(group);
      const system = s.system ?? v.system;
      const messages = system ? [{ role: "system", content: system }] : [];
      for (const [ti, t] of s.turns.entries()) {
        const think = force || t.think || s.think || v.think;
        turnsBox.append(
          el("div", { class: "turn-head", text: `턴 ${ti + 1} / ${s.turns.length}` }),
          el("div", { class: "msg user" }, el("div", { class: "bubble", text: t.user })));
        const view = assistantView(turnsBox, think);
        messages.push({ role: "user", content: t.user });
        let r;
        try {
          r = await callTurn(messages, think, view, scAbort.signal);
        } catch (err) {
          const aborted = err.name === "AbortError";
          view.fail(aborted ? "중지했습니다" : err.message);
          row.fails += s.turns.slice(ti).filter((x) => x.expect?.length).length;
          row.note = aborted ? "중지함" : err.message;
          if (aborted) throw err;
          break;
        }
        messages.push({ role: "assistant", content: r.content });
        row.done++;
        row.elapsed += r.elapsed_ms;
        if (t.expect?.length) {
          const miss = checkExpect(r.content, t.expect);
          if (miss.length) row.fails++;
          view.meta.prepend(el("span", { class: "chip " + (miss.length ? "fail" : "pass") },
            icon(miss.length ? "x-circle" : "check-circle"),
            miss.length ? `없음: ${miss.join(", ")}` : `PASS ${t.expect.join(", ")}`));
        }
        renderSummary(rows);
      }
      row.running = false;
      group.open = row.fails > 0;
      renderSummary(rows);
    }
  } catch (err) {
    if (err.name !== "AbortError") log.append(el("p", { class: "error-box", text: err.message }));
  } finally {
    rows.forEach((r) => { r.running = false; });
    renderSummary(rows);
    scAbort = null;
    $("#scRun").hidden = false;
    $("#scStop").hidden = true;
    if ($("#statusDot").classList.contains("busy")) setStatus("ok");
  }
});

$("#scStop").addEventListener("click", () => scAbort?.abort());

// ---- 탭 ----

function showTab(name) {
  for (const b of $$(".tabs button")) {
    const on = b.dataset.tab === name;
    b.setAttribute("aria-selected", String(on));
    $("#tab-" + b.dataset.tab).hidden = !on;
  }
  $("#resetChat").hidden = name !== "chat";
  store("tab", name);
}
for (const b of $$(".tabs button")) b.addEventListener("click", () => showTab(b.dataset.tab));
showTab(store("tab") === "scenario" ? "scenario" : "chat");

renderEmpty();
loadConfig()
  .then(() => refreshModels(true))
  .catch((err) => saveStatus("설정 불러오기 실패: " + err.message, "err"));
loadScenarios().catch((err) => $("#scList").replaceChildren(el("p", { class: "error-box", text: err.message })));
