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
// 데스크톱에서는 설정 패널을 접고 펴고(상태를 기억한다), 좁은 화면에서는 서랍을 연다.
const app = $(".app");
const narrow = matchMedia("(max-width: 900px)");
function setCollapsed(on) {
  app.classList.toggle("collapsed", on);
  $("#openSidebar").setAttribute("aria-expanded", String(!on));
  store("sidebar", on ? "hidden" : null);
}
function toggleSidebar() {
  if (narrow.matches) sidebar(!$("#sidebar").classList.contains("open"));
  else setCollapsed(!app.classList.contains("collapsed"));
}
$("#openSidebar").addEventListener("click", toggleSidebar);
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "b") {
    e.preventDefault();
    toggleSidebar();
  }
});
if (store("sidebar") === "hidden") setCollapsed(true);
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

// copyText 는 클립보드에 넣는다. http 로 다른 PC 에서 열면 navigator.clipboard 가 없으므로
// 숨긴 textarea 와 execCommand("copy") 로 대신한다.
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = el("textarea", { readonly: true, style: "position:fixed;left:-9999px;top:0" });
  ta.value = text;
  document.body.append(ta);
  ta.select();
  const ok = document.execCommand("copy");
  ta.remove();
  if (!ok) throw new Error("복사하지 못했습니다");
}

function downloadText(name, text) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
  const a = el("a", { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// 버튼에 잠깐 확인 표시를 띄운다.
function flash(btn, ok, label) {
  const old = [...btn.childNodes];
  btn.replaceChildren(icon(ok ? "check" : "x"), label);
  btn.classList.toggle("done", ok);
  setTimeout(() => { btn.replaceChildren(...old); btn.classList.remove("done"); }, 1400);
}

const EXT = {
  go: "go", golang: "go", python: "py", py: "py", javascript: "js", js: "js", jsx: "jsx", typescript: "ts", ts: "ts", tsx: "tsx",
  java: "java", kotlin: "kt", kt: "kt", swift: "swift", rust: "rs", rs: "rs", c: "c", cpp: "cpp", "c++": "cpp", csharp: "cs", cs: "cs",
  sql: "sql", sh: "sh", bash: "sh", shell: "sh", zsh: "sh", powershell: "ps1", ps1: "ps1", bat: "bat", cmd: "bat",
  json: "json", yaml: "yaml", yml: "yaml", toml: "toml", xml: "xml", html: "html", css: "css", markdown: "md", md: "md",
  dockerfile: "Dockerfile", makefile: "Makefile", php: "php", ruby: "rb", rb: "rb", scala: "scala", r: "r", lua: "lua", dart: "dart",
};

// fileName 은 저장할 이름을 정한다. hint(펜스 정보나 바로 앞 문장에 나온 파일 이름)가
// 이 언어의 확장자와 맞으면 그 이름을 쓰고, 아니면 snippet-N.확장자 로 한다.
function fileName(lang, n, hint) {
  const ext = EXT[lang.toLowerCase()] || "txt";
  const names = (hint || "").replace(/https?:\/\/\S+/g, "").match(/[\w.-]+\.[A-Za-z0-9]+/g) || [];
  const hit = names.reverse().find((x) => x.toLowerCase().endsWith("." + ext.toLowerCase()));
  if (hit) return hit.replace(/^[.-]+/, "");
  if (ext === "Dockerfile" || ext === "Makefile") return n > 1 ? `${ext}-${n}` : ext;
  return `snippet-${n}.${ext}`;
}

// codeBlock 은 머리줄(언어, 줄 수, 복사, 다운로드)이 붙은 코드 블록을 만든다.
function codeBlock(lang, code, n, hint) {
  const copy = el("button", { type: "button", class: "code-btn", title: "코드 복사" }, icon("copy"), "복사");
  const name = fileName(lang || "", n, hint);
  const save = el("button", { type: "button", class: "code-btn", title: name + " 로 저장" }, icon("download-simple"), name);
  copy.addEventListener("click", async () => {
    try { await copyText(code); flash(copy, true, "복사됨"); } catch (e) { flash(copy, false, "실패"); }
  });
  save.addEventListener("click", () => { downloadText(name, code.endsWith("\n") ? code : code + "\n"); flash(save, true, "저장함"); });
  const lines = code.split("\n").length;
  return el("div", { class: "code-block" },
    el("div", { class: "code-head" },
      el("span", { class: "code-lang", text: lang || "text" }), el("span", { class: "code-lines", text: `${lines}줄` }),
      el("span", { class: "spacer" }), copy, save),
    el("pre", {}, el("code", { text: code })));
}

// renderText 는 답변을 안전하게 그린다. ``` 코드 블록과 `인라인 코드`만 구분하고 나머지는 글자 그대로 둔다.
function renderText(container, text) {
  const nodes = [];
  const parts = text.split(/```/);
  let blocks = 0;
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      const nl = part.indexOf("\n");
      const info = nl >= 0 ? part.slice(0, nl).trim() : "";
      const lang = info.split(/\s+/)[0] || "";
      const body = nl >= 0 ? part.slice(nl + 1) : part;
      // 파일 이름 단서: 펜스 정보(```go main.go) 또는 블록 바로 앞 문단의 마지막 줄
      const before = (parts[i - 1] || "").trimEnd().split("\n").pop();
      nodes.push(codeBlock(lang, body.replace(/\n$/, ""), ++blocks, `${before} ${info.slice(lang.length)}`));
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
      // 모델이 추론 안에서 답을 끝내고 본문을 비우는 경우가 있다. 빈 칸으로 두지 않고 알린다.
      const emptyAnswer = !r.content.trim();
      if (emptyAnswer) {
        answer.replaceChildren(el("p", { class: "empty-answer", text: r.reasoning ? "본문 없이 추론만 왔습니다. 위 추론 과정 끝에 답이 있을 수 있어요." : "빈 응답이 왔습니다." }));
        details.open = !!r.reasoning;
      }
      const copy = el("button", { type: "button", class: "icon-btn copy", title: "답변 전체 복사", "aria-label": "답변 전체 복사" }, icon("copy"));
      copy.addEventListener("click", async () => {
        try { await copyText(r.content); copy.replaceChildren(icon("check")); } catch (e) { copy.replaceChildren(icon("x")); }
        setTimeout(() => copy.replaceChildren(icon("copy")), 1400);
      });
      meta.replaceChildren(...statChips(r, think), ...(emptyAnswer ? [el("span", { class: "chip warn", text: "본문 0자" })] : []), copy);
    },
    fail(msg) {
      answer.classList.remove("streaming");
      details.classList.remove("live");
      body.append(el("div", { class: "error-box", text: msg }));
    },
  };
}

// ---- 한 턴 호출 (SSE) ----

// meta 는 실행 기록용이다: { source, session, scenario, turn }
async function callTurn(messages, think, view, signal, meta = {}) {
  const v = formValues();
  const res = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      base_url: v.base_url, api_key: v.api_key, model: v.model, server: v.server, preset: v.preset,
      temperature: v.temperature, max_tokens: v.max_tokens,
      reasoning_effort: v.reasoning_effort, stream: v.stream,
      think, messages, meta,
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
let chatSession = newSession();

function newSession() {
  const b = new Uint8Array(4);
  crypto.getRandomValues(b);
  return [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
}
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
    }, chatAbort.signal, { source: "web-chat", session: chatSession });
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
  chatSession = newSession();
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
  // 통과율은 실제로 검사한 턴만 센다. 중지해서 못 돈 턴은 넣지 않는다.
  const checks = rows.reduce((a, r) => a + r.checked, 0);
  const passed = rows.reduce((a, r) => a + r.checked - r.fails, 0);
  const elapsed = rows.reduce((a, r) => a + r.elapsed, 0);
  const turns = rows.reduce((a, r) => a + r.done, 0);
  const okRows = rows.filter((r) => !r.running && !r.stopped && !r.fails).length;
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
      const state = r.running ? "" : r.fails ? "fail" : r.stopped ? "" : "pass";
      return el("div", { class: "board-row" },
        el("span", { text: r.name }),
        el("span", { class: "bar-cell" }, el("div", { class: "bar " + state }, el("span", { style: `width:${pct}%` }))),
        el("span", {}, r.running
          ? el("span", { class: "chip", text: `${r.done}/${r.turns}` })
          : r.stopped && !r.fails
            ? el("span", { class: "chip", text: `중지 ${r.done}/${r.turns}` })
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
    done: 0, checked: 0, fails: 0, elapsed: 0, note: "", running: true, stopped: false,
    started: false, server: "", results: [],
  }));
  const runSession = newSession();
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
        row.started = true;
        try {
          r = await callTurn(messages, think, view, scAbort.signal,
            { source: "web-scenario", session: runSession, scenario: s.name, turn: ti + 1 });
        } catch (err) {
          const aborted = err.name === "AbortError";
          view.fail(aborted ? "중지했습니다" : err.message);
          if (aborted) {
            row.stopped = true;
            throw err;
          }
          // 호출 오류는 남은 검사까지 실패로 센다.
          const left = s.turns.slice(ti).filter((x) => x.expect?.length).length || 1;
          row.checked += left;
          row.fails += left;
          row.note = err.message;
          row.results.push({ turn: ti + 1, expect: [], missing: ["(호출 오류)"] });
          break;
        }
        messages.push({ role: "assistant", content: r.content });
        row.done++;
        row.elapsed += r.elapsed_ms;
        row.server = r.server;
        if (t.expect?.length) {
          const miss = checkExpect(r.content, t.expect);
          row.checked++;
          row.results.push({ turn: ti + 1, expect: t.expect, missing: miss });
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
    rows.forEach((r, i) => {
      if (r.running && r.done < r.turns) r.stopped = true;
      r.running = false;
      if (!r.started) return;
      // 시나리오 요약을 실행 기록에 남긴다 (턴은 서버가 이미 남겼다).
      api("/api/runs/scenario", {
        session: runSession, name: r.name, base_url: v.base_url, model: v.model,
        server: r.server || v.server, think: force || "scenario",
        turns: r.turns, done: r.done, checks: r.checked, fails: r.fails, stopped: r.stopped,
        elapsed_ms: r.elapsed, note: r.note, results: r.results,
      }).catch((e) => console.warn("기록 실패", picked[i].name, e));
    });
    renderSummary(rows);
    scAbort = null;
    $("#scRun").hidden = false;
    $("#scStop").hidden = true;
    if ($("#statusDot").classList.contains("busy")) setStatus("ok");
  }
});

$("#scStop").addEventListener("click", () => scAbort?.abort());

// ---- 기록 탭 ----

let histRecords = [];
const avg = (xs) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null);
const fmtAvgSec = (ms) => (ms == null ? "-" : fmtSec(ms));
const fmtNum = (n) => (n == null ? "-" : Math.round(n).toLocaleString());

async function loadHistory() {
  const days = $("input[name=histDays]:checked").value;
  try {
    const r = await api(`/api/runs?days=${days}`);
    histRecords = r.items;
    $("#histDir").textContent = `기록 폴더 ${r.dir} · ${r.items.length.toLocaleString()}줄`;
  } catch (err) {
    histRecords = [];
    $("#histDir").textContent = "기록을 읽지 못했습니다: " + err.message;
  }
  const sel = $("#histModel");
  const cur = sel.value;
  const models = [...new Set(histRecords.map((x) => x.model).filter(Boolean))].sort();
  sel.replaceChildren(el("option", { value: "", text: "전체" }), ...models.map((m) => el("option", { value: m, text: m })));
  sel.value = models.includes(cur) ? cur : "";
  renderHistory();
}

function rateCell(passed, total) {
  if (!total) return el("td", { text: "-" });
  const pct = Math.round((passed / total) * 100);
  return el("td", {}, el("span", { class: "rate" },
    el("div", { class: "bar " + (passed === total ? "pass" : "fail") }, el("span", { style: `width:${pct}%` })),
    el("span", { class: "num", text: `${pct}% (${passed}/${total})` })));
}

function table(headers, rows) {
  return el("div", { class: "dtable-wrap" }, el("table", { class: "dtable" },
    el("thead", {}, el("tr", {}, ...headers.map(([t, cls]) => el("th", { class: cls || null, text: t })))),
    el("tbody", {}, ...rows)));
}

function renderHistory() {
  const model = $("#histModel").value;
  const source = $("#histSource").value;
  const pick = (x) => (!model || x.model === model) && (!source || x.source === source);
  const turns = histRecords.filter((x) => x.type === "turn" && pick(x));
  const scen = histRecords.filter((x) => x.type === "scenario" && pick(x));
  const ok = turns.filter((t) => !t.error);
  const checks = scen.reduce((a, s) => a + s.checks, 0);
  const passed = scen.reduce((a, s) => a + s.checks - s.fails, 0);
  const tile = (label, value, cls) => el("div", { class: "tile" }, el("small", { text: label }), el("b", { class: cls || null, text: value }));
  const body = $("#histBody");
  if (!turns.length && !scen.length) {
    body.replaceChildren(el("div", { class: "dtable-wrap" }, el("p", { class: "empty-note", text: "이 기간에 기록이 없습니다. 대화나 시나리오를 실행하면 쌓입니다." })));
    return;
  }

  const tiles = el("div", { class: "tiles" },
    tile("LLM 호출", `${turns.length.toLocaleString()}회${turns.length - ok.length ? ` · 오류 ${turns.length - ok.length}` : ""}`),
    tile("시나리오 실행", `${scen.length.toLocaleString()}회`),
    tile("검사 통과율", checks ? `${Math.round((passed / checks) * 100)}%` : "-", checks ? (passed === checks ? "pass" : "fail") : ""),
    tile("평균 TTFT", fmtAvgSec(avg(ok.filter((t) => t.ttft_ms).map((t) => t.ttft_ms)))));

  // 모델·thinking 별 호출 비교
  const groups = new Map();
  for (const t of turns) {
    const k = `${t.model}\u0000${t.think}`;
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(t);
  }
  const cmpRows = [...groups.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([k, ts]) => {
    const [m, think] = k.split("\u0000");
    const good = ts.filter((t) => !t.error);
    return el("tr", {},
      el("td", { text: m }), el("td", {}, el("span", { class: "chip", text: think })),
      el("td", { class: "num", text: ts.length.toLocaleString() }),
      el("td", { class: "num", text: fmtAvgSec(avg(good.map((t) => t.elapsed_ms))) }),
      el("td", { class: "num", text: fmtAvgSec(avg(good.filter((t) => t.ttft_ms).map((t) => t.ttft_ms))) }),
      el("td", { class: "num", text: fmtNum(avg(good.filter((t) => t.usage).map((t) => t.usage.completion_tokens))) }),
      el("td", { class: "num", text: fmtNum(avg(good.map((t) => [...(t.reasoning || "")].length))) }),
      el("td", { class: "num", text: String(ts.length - good.length) }));
  });

  // 시나리오 통과율 (모델 × thinking 강제값)
  const sg = new Map();
  for (const s of scen) {
    const k = `${s.model}\u0000${s.think}`;
    if (!sg.has(k)) sg.set(k, []);
    sg.get(k).push(s);
  }
  const scRows = [...sg.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([k, ss]) => {
    const [m, think] = k.split("\u0000");
    const c = ss.reduce((a, s) => a + s.checks, 0);
    const p = ss.reduce((a, s) => a + s.checks - s.fails, 0);
    const done = ss.reduce((a, s) => a + s.done, 0);
    return el("tr", {},
      el("td", { text: m }), el("td", {}, el("span", { class: "chip", text: think === "scenario" ? "시나리오 값" : think })),
      el("td", { class: "num", text: String(ss.length) }), rateCell(p, c),
      el("td", { class: "num", text: done ? fmtSec(ss.reduce((a, s) => a + s.elapsed_ms, 0) / done) : "-" }));
  });

  // 최근 시나리오 실행
  const recentSc = scen.slice(-30).reverse().map((s) => el("tr", {},
    el("td", { class: "num", text: s.time.slice(5, 19) }),
    el("td", { text: s.name }), el("td", { text: s.model }),
    el("td", {}, el("span", { class: "chip", text: s.think === "scenario" ? "시나리오 값" : s.think })),
    el("td", {}, s.stopped && !s.fails
      ? el("span", { class: "chip", text: `중지 ${s.done}/${s.turns}` })
      : el("span", { class: "chip " + (s.fails ? "fail" : "pass") }, icon(s.fails ? "x-circle" : "check-circle"), s.fails ? `FAIL ${s.fails}` : "PASS")),
    el("td", { class: "num", text: `${s.checks - s.fails}/${s.checks}` }),
    el("td", { class: "num", text: fmtSec(s.elapsed_ms) }),
    el("td", {}, el("span", { class: "chip", text: s.source }))));

  // 최근 호출 (클릭하면 질문·답변·추론)
  const recentTurns = [];
  for (const t of turns.slice(-100).reverse()) {
    const row = el("tr", { class: "clickable" },
      el("td", { class: "num", text: t.time.slice(5, 19) }),
      el("td", {}, el("span", { class: "chip", text: t.source })),
      el("td", { text: t.model }),
      el("td", {}, el("span", { class: "chip", text: t.think })),
      el("td", { class: "wrap", text: (t.scenario ? `[${t.scenario} #${t.turn}] ` : "") + t.user.slice(0, 80) }),
      el("td", { class: "num", text: t.error ? "오류" : fmtSec(t.elapsed_ms) }),
      el("td", { class: "num", text: t.usage ? `${t.usage.prompt_tokens}→${t.usage.completion_tokens}` : "-" }));
    if (t.error) row.querySelector("td:nth-child(6)").style.color = "var(--fail)";
    const detail = el("tr", { class: "turn-detail", hidden: true }, el("td", { colspan: "7" },
      el("b", { text: "질문" }), el("pre", { text: t.user }),
      t.reasoning ? el("b", { text: `추론 (${[...t.reasoning].length}자)` }) : null, t.reasoning ? el("pre", { text: t.reasoning }) : null,
      el("b", { text: t.error ? "오류" : "답변" }), el("pre", { text: t.error || t.content || "(본문 없음)" }),
      el("b", { text: "설정" }), el("pre", { text: `${t.server} · ${t.base_url} · temperature ${t.temperature} · max_tokens ${t.max_tokens}${t.reasoning_effort ? " · effort " + t.reasoning_effort : ""}${t.finish ? " · finish " + t.finish : ""}` })));
    row.addEventListener("click", () => { detail.hidden = !detail.hidden; });
    recentTurns.push(row, detail);
  }

  body.replaceChildren(tiles,
    el("div", { class: "section-title" }, icon("chart-bar"), "모델 · thinking 별 호출 비교"),
    table([["모델"], ["thinking"], ["호출", "num"], ["평균 소요", "num"], ["평균 TTFT", "num"], ["평균 completion tok", "num"], ["평균 추론자", "num"], ["오류", "num"]], cmpRows),
    el("div", { class: "section-title" }, icon("list-checks"), "시나리오 통과율"),
    scRows.length ? table([["모델"], ["thinking"], ["실행", "num"], ["검사 통과율"], ["턴 평균", "num"]], scRows)
      : el("div", { class: "dtable-wrap" }, el("p", { class: "empty-note", text: "시나리오 기록이 없습니다." })),
    recentSc.length ? el("div", { class: "section-title" }, icon("clock-counter-clockwise"), "최근 시나리오 실행 (30)") : null,
    recentSc.length ? table([["시각", "num"], ["시나리오"], ["모델"], ["thinking"], ["결과"], ["검사", "num"], ["소요", "num"], ["출처"]], recentSc) : null,
    el("div", { class: "section-title" }, icon("chat-circle-dots"), "최근 호출 (100) · 줄을 누르면 질문·답변·추론"),
    table([["시각", "num"], ["출처"], ["모델"], ["thinking"], ["질문"], ["소요", "num"], ["토큰", "num"]], recentTurns));
}

for (const r of $$("input[name=histDays]")) r.addEventListener("change", loadHistory);
$("#histModel").addEventListener("change", renderHistory);
$("#histSource").addEventListener("change", renderHistory);
$("#histReload").addEventListener("click", loadHistory);

// ---- 로그 탭 ----

const logList = $("#logList");
let logCursor = 0;
let logShown = 0;
let bootSeen = null;

function logRow(e) {
  const cls = `logrow kind-${e.kind} level-${e.level}`;
  const head = [
    el("time", { text: e.time.slice(11) , title: e.time }),
    el("span", { class: "kind", text: e.kind }),
    el("span", { class: "sum", text: e.summary }),
  ];
  if (!e.detail) return el("div", { class: cls + " plain" }, ...head, el("span"));
  return el("details", { class: cls }, el("summary", {}, ...head, icon("caret-down", "caret")), el("pre", { text: e.detail }));
}

async function pollLogs() {
  try {
    let r = await api(`/api/logs?after=${logCursor}`);
    if (bootSeen && r.boot !== bootSeen) {
      // 서버가 다시 떴다 (새 버전 배포). 로그 번호가 1부터 다시 시작하므로 처음부터 받는다.
      $("#updateTime").textContent = r.boot;
      $("#updateBar").hidden = false;
      logList.append(el("div", { class: "logrow kind-http level-info plain" },
        el("time", { text: "──" }), el("span", { class: "kind", text: "boot" }), el("span", { class: "sum", text: `서버 재시작 ${r.boot}` }), el("span")));
      logCursor = 0;
      r = await api(`/api/logs?after=0`);
    }
    bootSeen = r.boot;
    if (r.items.length) {
      const nearBottom = logScroller.scrollHeight - logScroller.scrollTop - logScroller.clientHeight < 80;
      logList.append(...r.items.map(logRow));
      logCursor = r.items[r.items.length - 1].id;
      logShown += r.items.length;
      // 화면에 너무 많이 쌓이면 오래된 줄부터 지운다 (서버도 500건만 들고 있다).
      while (logList.children.length > 500) logList.firstChild.remove();
      $("#logCount").textContent = `${logList.children.length}줄`;
      const logsOpen = !$("#tab-logs").hidden;
      if (!logsOpen && r.items.some((e) => e.level === "error")) $("#logDot").hidden = false;
      if (logsOpen && $("#logFollow").checked && nearBottom) logScroller.scrollTop = logScroller.scrollHeight;
    }
  } catch (e) { /* 서버가 내려가 있으면 다음 주기에 다시 본다 */ }
  setTimeout(pollLogs, 1500);
}
const logScroller = $("#tab-logs");

for (const r of $$("input[name=logFilter]")) {
  r.addEventListener("change", () => { logList.dataset.filter = r.value; });
}
$("#reloadBtn").addEventListener("click", () => location.reload());
$("#updateClose").addEventListener("click", () => { $("#updateBar").hidden = true; });
$("#logClear").addEventListener("click", () => { logList.replaceChildren(); $("#logCount").textContent = ""; });
$("#logFollow").addEventListener("change", (e) => { if (e.target.checked) logScroller.scrollTop = logScroller.scrollHeight; });

// ---- 탭 ----

function showTab(name) {
  for (const b of $$(".tabs button")) {
    const on = b.dataset.tab === name;
    b.setAttribute("aria-selected", String(on));
    $("#tab-" + b.dataset.tab).hidden = !on;
  }
  $("#resetChat").hidden = name !== "chat";
  if (name === "history") loadHistory();
  if (name === "logs") {
    $("#logDot").hidden = true;
    if ($("#logFollow").checked) logScroller.scrollTop = logScroller.scrollHeight;
  }
  store("tab", name);
}
for (const b of $$(".tabs button")) b.addEventListener("click", () => showTab(b.dataset.tab));
showTab(["scenario", "history", "logs"].includes(store("tab")) ? store("tab") : "chat");

renderEmpty();
pollLogs();
loadConfig()
  .then(() => refreshModels(true))
  .catch((err) => saveStatus("설정 불러오기 실패: " + err.message, "err"));
loadScenarios().catch((err) => $("#scList").replaceChildren(el("p", { class: "error-box", text: err.message })));
