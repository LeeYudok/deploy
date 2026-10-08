package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	logKeep        = 500       // 메모리에 두는 로그 수
	logBodyLimit   = 16 * 1024 // 요청 JSON 을 이 길이에서 자른다
	logAnswerLimit = 600       // 답변 미리보기 길이
)

// LogEntry 는 웹 UI 로그 탭의 한 줄이다.
type LogEntry struct {
	ID      int64  `json:"id"`
	Time    string `json:"time"`  // YYYY-MM-DD HH:MM:SS.mmm
	Kind    string `json:"kind"`  // http | llm
	Level   string `json:"level"` // info | error
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

// logBuf 는 최근 로그를 링 버퍼로 들고 있고, 같은 줄을 stdout 에도 찍는다.
type logBuf struct {
	mu    sync.Mutex
	next  int64
	items []LogEntry
	boot  string // 서버를 띄운 시각. 화면이 재시작(새 버전)을 알아채는 데 쓴다
}

func (l *logBuf) add(kind, level, summary, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	e := LogEntry{ID: l.next, Time: time.Now().Format("2006-01-02 15:04:05.000"), Kind: kind, Level: level, Summary: summary, Detail: detail}
	l.items = append(l.items, e)
	if len(l.items) > logKeep {
		l.items = l.items[len(l.items)-logKeep:]
	}
	fmt.Printf("%s %-5s %-4s %s\n", e.Time, strings.ToUpper(level), kind, summary)
}

func (l *logBuf) since(id int64) []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []LogEntry{}
	for _, e := range l.items {
		if e.ID > id {
			out = append(out, e)
		}
	}
	return out
}

// trace 는 업스트림 호출 기록을 로그 한 줄로 만든다.
func (l *logBuf) trace(t Trace) {
	level := "info"
	if t.Err != nil || (t.Status != 0 && t.Status != http.StatusOK) {
		level = "error"
	}
	path := t.URL
	if i := strings.Index(path, "/v1/"); i >= 0 {
		path = path[i+3:]
	}
	parts := []string{t.Method + " " + path, statusText(t.Status)}

	var req struct {
		Model              string          `json:"model"`
		Messages           []Message       `json:"messages"`
		Stream             bool            `json:"stream"`
		ChatTemplateKwargs map[string]bool `json:"chat_template_kwargs"`
		ReasoningEffort    string          `json:"reasoning_effort"`
	}
	if len(t.Request) > 0 && json.Unmarshal(t.Request, &req) == nil {
		parts = append(parts, req.Model, fmt.Sprintf("메시지 %d", len(req.Messages)))
		if req.ChatTemplateKwargs != nil {
			parts = append(parts, fmt.Sprintf("thinking=%v", req.ChatTemplateKwargs["thinking"]))
		}
		if req.ReasoningEffort != "" {
			parts = append(parts, "effort="+req.ReasoningEffort)
		}
		if req.Stream {
			parts = append(parts, "stream")
		}
	}
	parts = append(parts, t.Elapsed.Round(time.Millisecond).String())
	if r := t.Result; r != nil {
		if r.TTFT > 0 {
			parts = append(parts, "TTFT "+r.TTFT.Round(time.Millisecond).String())
		}
		if u := r.Usage; u != nil {
			parts = append(parts, fmt.Sprintf("%d→%d tok", u.PromptTokens, u.CompletionTokens))
		}
		parts = append(parts, fmt.Sprintf("추론 %d자", len([]rune(r.Reasoning))), fmt.Sprintf("본문 %d자", len([]rune(r.Content))))
		if r.Finish != "" && r.Finish != "stop" {
			parts = append(parts, "finish="+r.Finish)
		}
	}
	if t.Method == http.MethodGet && t.Err == nil {
		parts = append(parts, fmt.Sprintf("모델 %d개", t.Models))
	}
	if t.Err != nil {
		parts = append(parts, oneLine(t.Err.Error(), 160))
	}

	var d strings.Builder
	fmt.Fprintf(&d, "%s %s\n", t.Method, t.URL)
	if len(t.Request) > 0 {
		d.WriteString("\n[요청]\n")
		d.WriteString(prettyJSON(t.Request, logBodyLimit))
		d.WriteString("\n")
	}
	d.WriteString("\n[응답]\n")
	fmt.Fprintf(&d, "상태 %s, 소요 %s\n", statusText(t.Status), t.Elapsed.Round(time.Millisecond))
	if r := t.Result; r != nil {
		if r.TTFT > 0 {
			fmt.Fprintf(&d, "첫 토큰 %s\n", r.TTFT.Round(time.Millisecond))
		}
		if r.Usage != nil {
			u, _ := json.Marshal(r.Usage)
			fmt.Fprintf(&d, "usage %s\n", u)
		}
		if r.Finish != "" {
			fmt.Fprintf(&d, "finish_reason %s\n", r.Finish)
		}
		fmt.Fprintf(&d, "추론 %d자, 본문 %d자\n", len([]rune(r.Reasoning)), len([]rune(r.Content)))
		if r.Reasoning != "" {
			fmt.Fprintf(&d, "\n[추론 미리보기]\n%s\n", clip(r.Reasoning, logAnswerLimit))
		}
		fmt.Fprintf(&d, "\n[본문 미리보기]\n%s\n", clip(r.Content, logAnswerLimit))
	}
	if t.Err != nil {
		fmt.Fprintf(&d, "\n[오류]\n%s\n", t.Err)
	}
	l.add("llm", level, strings.Join(nonEmpty(parts), " · "), d.String())
}

func statusText(code int) string {
	if code == 0 {
		return "연결 실패"
	}
	return strconv.Itoa(code)
}

func prettyJSON(b []byte, limit int) string {
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") != nil {
		out.Reset()
		out.Write(b)
	}
	return clip(out.String(), limit)
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + fmt.Sprintf("\n... (%d자 중 %d자만 표시)", len(r), limit)
}

func oneLine(s string, limit int) string {
	return clip(strings.Join(strings.Fields(s), " "), limit)
}

func nonEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// statusRecorder 는 응답 상태 코드를 기록한다. SSE 를 위해 Flush 를 그대로 넘긴다.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logHTTP 는 화면이 부른 /api/* 를 로그에 남긴다. 로그 조회 자체는 남기지 않는다.
func (l *logBuf) logHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/logs" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		level := "info"
		if rec.status >= 400 {
			level = "error"
		}
		l.add("http", level, fmt.Sprintf("%s %s · %d · %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond)), "")
	})
}

// handleLogs 는 after 보다 뒤의 로그를 준다. 화면이 주기적으로 부른다.
func (l *logBuf) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	writeJSON(w, http.StatusOK, map[string]any{"items": l.since(after), "boot": l.boot})
}
