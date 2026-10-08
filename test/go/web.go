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
	scenarios string // 시나리오 파일·폴더 (쉼표로 여러 개)
}

func runWeb(addr string, open bool, cfgPath string, cfg map[string]string, s Settings, timeout time.Duration, scenarios string) error {
	if cfgPath == "" {
		// 설정파일이 없으면 test/ 폴더(시나리오 폴더 옆)에, 그것도 없으면 현재 폴더에 만든다.
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
	ws := &webServer{settings: s, cfgPath: cfgPath, cfg: cfg, client: &http.Client{Timeout: timeout}, scenarios: scenarios}

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
	srv := &http.Server{Handler: guard(mux, loopback), ReadHeaderTimeout: 10 * time.Second}
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
}

func (ws *webServer) options(c connReq) Options {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	o := Options{Client: ws.client, Base: strings.TrimRight(c.BaseURL, "/"), APIKey: c.APIKey}
	if o.Base == "" {
		o.Base = strings.TrimRight(ws.settings.BaseURL, "/")
	}
	if o.APIKey == "" {
		o.APIKey = ws.settings.APIKey
	}
	return o
}

func (ws *webServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.mu.Lock()
		out := struct {
			Settings
			APIKeySet  bool   `json:"api_key_set"`
			ConfigPath string `json:"config_path"`
		}{ws.settings, ws.settings.APIKey != "", ws.cfgPath}
		ws.mu.Unlock()
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var in struct {
			Settings
			APIKey      string `json:"api_key"`
			ClearAPIKey bool   `json:"clear_api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, err := thinkKwargs(in.Think); err != nil {
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
		s.APIKey = ws.settings.APIKey
		if in.ClearAPIKey {
			s.APIKey = ""
		} else if in.APIKey != "" {
			s.APIKey = in.APIKey
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
		llm["think"] = s.Think
		llm["reasoning_effort"] = s.Effort
		llm["temperature"] = strconv.FormatFloat(s.Temperature, 'f', -1, 64)
		llm["max_tokens"] = strconv.Itoa(s.MaxTokens)
		llm["system"] = s.System
		if err := saveToml(ws.cfgPath, llm); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		ws.settings = s
		ws.cfg = map[string]string{}
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
	list, err := listModels(r.Context(), opt)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
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
	})
}
