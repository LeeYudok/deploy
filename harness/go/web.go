package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFiles embed.FS

// Settings 는 웹 UI 에서 바꾸는 설정이다. API 키는 브라우저로 내보내지 않는다.
type Settings struct {
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	APIKey      string  `json:"-"`
	Server      string  `json:"server"`
	Think       string  `json:"think"`
	Effort      string  `json:"reasoning_effort"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	System      string  `json:"system"`
}

type webServer struct {
	mu        sync.Mutex
	settings  Settings
	cfgPath   string            // 저장할 .env.toml 경로
	cfg       map[string]string // 읽은 설정 원본 (모르는 키를 보존하려고 둔다)
	client    *http.Client
	scenarios string            // 시나리오 파일·폴더 (쉼표로 여러 개)
	detected  map[string]string // base_url → 판별한 서버 종류
	presets   []Preset          // .env.toml 의 [preset.*]
	prices    map[string]Price  // .env.toml 의 [price."<모델>"]
	logs      *logBuf           // 로그 탭과 stdout
	runs      *runStore         // 실행 기록 (runs/*.jsonl)
}

func runWeb(addr string, open bool, cfgPath string, cfg map[string]string, s Settings, timeout time.Duration, scenarios string) error {
	if cfgPath == "" {
		// 설정파일이 없으면 harness/ 폴더(시나리오 폴더 옆)에, 그것도 없으면 현재 폴더에 만든다.
		if dir := findUp("scenarios", true); dir != "" {
			cfgPath = filepath.Join(filepath.Dir(dir), ".env.toml")
		} else {
			cfgPath = ".env.toml"
		}
	}
	if scenarios == "" {
		scenarios = findUp("scenarios", true)
	}
	if abs, err := filepath.Abs(cfgPath); err == nil {
		cfgPath = abs
	}
	ws := &webServer{settings: s, cfgPath: cfgPath, cfg: cfg, client: &http.Client{Timeout: timeout}, scenarios: scenarios, detected: map[string]string{}, presets: presetsFrom(cfg), prices: pricesFrom(cfg), logs: &logBuf{boot: time.Now().Format("2006-01-02 15:04:05.000")}}

	ws.runs = newRunStore(runsDir(), func(msg string) { ws.logs.add("runs", "warn", msg, "") })
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/config", ws.handleConfig)
	mux.HandleFunc("/api/models", ws.handleModels)
	mux.HandleFunc("/api/scenarios", ws.handleScenarios)
	mux.HandleFunc("/api/chat", ws.handleChat)
	mux.HandleFunc("/api/logs", ws.logs.handleLogs)
	mux.HandleFunc("/api/runs", ws.handleRuns)
	mux.HandleFunc("/api/runs/scenario", ws.handleRunScenario)
	mux.HandleFunc("/api/bench", ws.handleBench)
	mux.HandleFunc("/api/prices", ws.handlePrices)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	loopback := net.ParseIP(host).IsLoopback()
	urlHost := host
	if !loopback {
		urlHost = "127.0.0.1"
		fmt.Fprintf(os.Stderr, "주의: %s 로 열었습니다. 같은 망의 다른 PC 도 이 화면으로 LLM 을 호출할 수 있습니다.\n", ln.Addr())
	}
	url := "http://" + net.JoinHostPort(urlHost, port) + "/"
	fmt.Fprintf(os.Stderr, "웹 UI: %s  (설정파일 %s, 종료는 Ctrl+C)\n", url, cfgPath)
	if open {
		openBrowser(url)
	}
	ws.logs.add("http", "info", "웹 UI 시작 "+url, "")
	srv := &http.Server{Handler: guard(ws.logs.logHTTP(mux), loopback), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// guard 는 다른 사이트가 브라우저를 통해 이 서버를 부르는 것을 막는다.
// 루프백으로 열었으면 Host 헤더가 루프백인지 보고(DNS 리바인딩 차단),
// POST 는 application/json 만 받는다(폼 전송으로 들어오는 요청 차단).
func guard(next http.Handler, loopback bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loopback {
			h := r.Host
			if hh, _, err := net.SplitHostPort(h); err == nil {
				h = hh
			}
			if ip := net.ParseIP(strings.Trim(h, "[]")); !(h == "localhost" || (ip != nil && ip.IsLoopback())) {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodPost {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// connReq 는 화면의 접속 정보다. api_key 가 비어 있으면 서버에 있는 키를 쓴다.
type connReq struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`  // 모델 목록 조회에서 서버를 판별할 때 이 모델의 owned_by 를 본다
	Preset  string `json:"preset"` // 고른 프리셋 id (그 프리셋의 키를 쓴다)
}

func sameBase(a, b string) bool {
	return a != "" && strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// storedKey 는 저장된 키 중 이 접속주소에 쓸 키를 고른다.
// 키는 그 키가 저장된 접속주소로만 보낸다 — 화면에서 주소를 바꿨는데 다른 서버의 키가 따라가면 안 된다.
func (ws *webServer) storedKey(base, preset string) string {
	if p, ok := findPreset(ws.presets, preset); ok && sameBase(p.BaseURL, base) {
		return p.APIKey
	}
	if sameBase(ws.settings.BaseURL, base) {
		return ws.settings.APIKey
	}
	return ""
}

func (ws *webServer) options(c connReq) Options {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	o := Options{Client: ws.client, Base: strings.TrimRight(c.BaseURL, "/"), APIKey: c.APIKey, Trace: ws.logs.trace}
	if o.Base == "" {
		o.Base = strings.TrimRight(ws.settings.BaseURL, "/")
	}
	if o.APIKey == "" {
		o.APIKey = ws.storedKey(o.Base, c.Preset)
	}
	return o
}

func (ws *webServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.mu.Lock()
		type presetView struct {
			Preset
			APIKeySet bool `json:"api_key_set"`
		}
		views := make([]presetView, 0, len(ws.presets))
		for _, p := range ws.presets {
			views = append(views, presetView{p, p.APIKey != ""})
		}
		out := struct {
			Settings
			APIKeySet  bool             `json:"api_key_set"`
			ConfigPath string           `json:"config_path"`
			Presets    []presetView     `json:"presets"`
			Prices     map[string]Price `json:"prices"`
		}{ws.settings, ws.settings.APIKey != "", ws.cfgPath, views, ws.prices}
		ws.mu.Unlock()
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var in struct {
			Settings
			APIKey      string `json:"api_key"`
			ClearAPIKey bool   `json:"clear_api_key"`
			Preset      string `json:"preset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, _, err := parseThink(in.Think); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		var err error
		if in.Server, err = normalizeServer(in.Server); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.BaseURL) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("접속주소(base_url)를 입력하세요"))
			return
		}
		ws.mu.Lock()
		defer ws.mu.Unlock()
		s := in.Settings
		s.BaseURL = strings.TrimSpace(s.BaseURL)
		switch {
		case in.ClearAPIKey:
			s.APIKey = ""
		case in.APIKey != "":
			s.APIKey = in.APIKey
		default:
			s.APIKey = ws.storedKey(s.BaseURL, in.Preset)
		}

		llm := map[string]string{}
		for k, v := range ws.cfg {
			if rest, ok := strings.CutPrefix(k, "llm."); ok {
				llm[rest] = v
			} else if !strings.Contains(k, ".") {
				llm[k] = v
			}
		}
		llm["base_url"] = s.BaseURL
		llm["model"] = s.Model
		llm["api_key"] = s.APIKey
		llm["server"] = s.Server
		llm["think"] = s.Think
		llm["reasoning_effort"] = s.Effort
		llm["temperature"] = strconv.FormatFloat(s.Temperature, 'f', -1, 64)
		llm["max_tokens"] = strconv.Itoa(s.MaxTokens)
		llm["system"] = s.System
		if err := saveToml(ws.cfgPath, llm, ws.presets, ws.prices); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		ws.settings = s
		for k, v := range llm {
			ws.cfg["llm."+k] = v
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": ws.cfgPath, "api_key_set": s.APIKey != ""})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (ws *webServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var c connReq
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	opt := ws.options(c)
	if opt.Base == "" {
		writeErr(w, http.StatusBadRequest, errors.New("접속주소(base_url)를 입력하세요"))
		return
	}
	// 상태 확인용이라 오래 기다리지 않는다 (닿지 않는 주소면 -timeout 300s 를 다 기다리게 된다).
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := listModels(ctx, opt)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// 화면 상태 표시용으로 서버 종류도 같이 준다. 판별 결과는 대화 요청에서도 다시 쓴다.
	owner := ""
	for _, m := range list {
		if m.ID == c.Model || owner == "" {
			owner = m.OwnedBy
		}
	}
	server := serverFromOwner(owner)
	ws.mu.Lock()
	ws.detected[opt.Base] = server
	ws.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"models": list, "server": server, "owned_by": owner})
}

func (ws *webServer) handleScenarios(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	type item struct {
		File string `json:"file"`
		Scenario
	}
	out := struct {
		Source string   `json:"source"`
		Items  []item   `json:"items"`
		Errors []string `json:"errors"`
	}{Source: ws.scenarios, Items: []item{}, Errors: []string{}}
	if ws.scenarios == "" {
		out.Errors = append(out.Errors, "scenarios 폴더를 찾지 못했습니다 (-scenario 로 지정)")
		writeJSON(w, http.StatusOK, out)
		return
	}
	files, err := scenarioFiles(ws.scenarios)
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
	}
	for _, f := range files {
		sc, err := loadScenario(f)
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
			continue
		}
		out.Items = append(out.Items, item{File: filepath.Base(f), Scenario: sc})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleChat 은 한 턴을 호출하고 결과를 SSE(text/event-stream)로 흘려보낸다.
// 이벤트: reasoning, content (data 는 JSON 문자열), done (결과 JSON), error (메시지).
func (ws *webServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		connReq
		Messages    []Message `json:"messages"`
		Model       string    `json:"model"`
		Temperature float64   `json:"temperature"`
		MaxTokens   int       `json:"max_tokens"`
		Think       string    `json:"think"`
		Effort      string    `json:"reasoning_effort"`
		Stream      bool      `json:"stream"`
		Server      string    `json:"server"`
		Meta        struct {
			Source   string `json:"source"` // web-chat | web-scenario
			Session  string `json:"session"`
			Scenario string `json:"scenario"`
			Turn     int    `json:"turn"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	opt := ws.options(in.connReq)
	opt.Model, opt.Temp, opt.MaxTokens, opt.Effort, opt.Stream = in.Model, in.Temperature, in.MaxTokens, in.Effort, in.Stream
	if opt.Base == "" || opt.Model == "" {
		writeErr(w, http.StatusBadRequest, errors.New("접속주소와 모델명을 입력하세요"))
		return
	}
	var err error
	if opt.Server, err = normalizeServer(in.Server); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if opt.Server == ServerAuto {
		opt.Server = ws.detect(r.Context(), opt)
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	res, err := complete(r.Context(), opt, in.Messages, in.Think, func(kind, text string) { send(kind, text) })
	if !errors.Is(err, context.Canceled) {
		// 중지한 턴은 남기지 않는다. 오류도 기록해 두면 서버별 실패율을 볼 수 있다.
		src := in.Meta.Source
		if src == "" {
			src = "web-chat"
		}
		rec := turnRecord(src, in.Meta.Session, opt, in.Think, in.Messages, res, err)
		rec.Scenario, rec.Turn = in.Meta.Scenario, in.Meta.Turn
		if werr := ws.runs.append(rec); werr != nil {
			ws.logs.add("runs", "error", "기록 쓰기 실패: "+werr.Error(), "")
		}
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			send("error", err.Error())
		}
		return
	}
	send("done", map[string]any{
		"content":    res.Content,
		"reasoning":  res.Reasoning,
		"finish":     res.Finish,
		"usage":      res.Usage,
		"ttft_ms":    res.TTFT.Milliseconds(),
		"elapsed_ms": res.Elapsed.Milliseconds(),
		"server":     res.Server,
	})
}

// detect 는 base_url 별로 서버 종류를 한 번만 판별해 둔다. 판별에 실패하면 저장하지 않고 다음에 다시 본다.
func (ws *webServer) detect(ctx context.Context, opt Options) string {
	ws.mu.Lock()
	s, ok := ws.detected[opt.Base]
	ws.mu.Unlock()
	if ok {
		return s
	}
	s, _, err := detectServer(ctx, opt)
	if err == nil {
		ws.mu.Lock()
		ws.detected[opt.Base] = s
		ws.mu.Unlock()
	}
	return s
}

// handleRuns 는 최근 실행 기록을 준다. days=0 이면 전부.
func (ws *webServer) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 0 {
		days = 7
	}
	items, err := ws.runs.load(days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	dir, _ := filepath.Abs(ws.runs.dir)
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir, "items": items})
}

// handleRunScenario 는 화면이 돌린 시나리오의 요약을 기록한다 (턴은 handleChat 이 남긴다).
func (ws *webServer) handleRunScenario(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var rec ScenarioRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rec.Type = "scenario"
	rec.Time = time.Now().Format(timeFmt)
	if rec.Source == "" {
		rec.Source = "web-scenario"
	}
	if err := ws.runs.append(rec); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleBench 는 부하 테스트를 돌리고 진행을 SSE 로 보낸다 (issue #17).
// 이벤트: sample (요청 한 건), level (수준 요약), done ({levels, recommend}), error.
func (ws *webServer) handleBench(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		connReq
		Model       string  `json:"model"`
		Server      string  `json:"server"`
		Think       string  `json:"think"`
		Effort      string  `json:"reasoning_effort"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		Levels      string  `json:"levels"`
		Requests    int     `json:"requests"`
		Prompt      string  `json:"prompt"`
		System      string  `json:"system"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	levels, err := parseLevels(in.Levels)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("프롬프트를 입력하세요"))
		return
	}
	if in.Requests < 0 || in.Requests > 1000 {
		writeErr(w, http.StatusBadRequest, errors.New("수준당 요청 수는 0~1000"))
		return
	}
	opt := ws.options(in.connReq)
	opt.Model, opt.Temp, opt.MaxTokens, opt.Effort = in.Model, in.Temperature, in.MaxTokens, in.Effort
	if opt.Base == "" || opt.Model == "" {
		writeErr(w, http.StatusBadRequest, errors.New("접속주소와 모델명을 입력하세요"))
		return
	}
	if opt.Server, err = normalizeServer(in.Server); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if opt.Server == ServerAuto {
		opt.Server = ws.detect(r.Context(), opt)
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	ws.logs.add("bench", "info", fmt.Sprintf("부하 테스트 시작 · %s · 동시 %v · 수준당 요청 %d · max_tokens %d · think=%s", opt.Model, levels, in.Requests, opt.MaxTokens, in.Think), "")
	cfg := BenchConfig{Levels: levels, Requests: in.Requests, Prompt: in.Prompt, System: in.System, Think: in.Think}
	res, err := runBench(r.Context(), opt, cfg,
		func(s BenchSample) { send("sample", s) },
		func(b BenchLevel) {
			level := "info"
			if b.Errors > 0 {
				level = "error"
			}
			ws.logs.add("bench", level, fmt.Sprintf("동시 %d · 요청 %d · 오류 %d · %.1f tok/s · p95 %dms · TTFT p95 %dms", b.Level, b.Requests, b.Errors, b.TokPerSec, b.LatP95ms, b.TTFTP95ms), b.FirstError)
			send("level", b)
		})
	rec := recommendLevel(res)
	stopped := errors.Is(err, context.Canceled)
	if len(res) > 0 {
		werr := ws.runs.append(BenchRecord{Type: "bench", Time: time.Now().Format(timeFmt), Source: "web-bench", BaseURL: opt.Base, Model: opt.Model,
			Server: opt.Server, Think: in.Think, MaxTokens: opt.MaxTokens, Prompt: in.Prompt, Requests: in.Requests, Levels: res, Recommend: rec, Stopped: stopped})
		if werr != nil {
			ws.logs.add("runs", "error", "기록 쓰기 실패: "+werr.Error(), "")
		}
	}
	if err != nil && !stopped {
		send("error", err.Error())
		return
	}
	if stopped {
		return
	}
	ws.logs.add("bench", "info", fmt.Sprintf("부하 테스트 끝 · 권장 동시 수 %d", rec), "")
	send("done", map[string]any{"levels": res, "recommend": rec, "server": opt.Server})
}

// handlePrices 는 모델 하나의 단가를 저장한다. input·output 이 둘 다 0 이면 지운다 (issue #18).
func (ws *webServer) handlePrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Model string `json:"model"`
		Price
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Model) == "" || in.Input < 0 || in.Output < 0 || in.Input > 1e6 || in.Output > 1e6 {
		writeErr(w, http.StatusBadRequest, errors.New("모델 이름과 0 이상의 단가를 넣으세요"))
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if in.Price.set() {
		ws.prices[in.Model] = in.Price
	} else {
		delete(ws.prices, in.Model)
	}
	llm := map[string]string{}
	for k, v := range ws.cfg {
		if rest, ok := strings.CutPrefix(k, "llm."); ok {
			llm[rest] = v
		}
	}
	if err := saveToml(ws.cfgPath, llm, ws.presets, ws.prices); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, ws.prices)
}
