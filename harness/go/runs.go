package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 실행 기록은 runs/YYYY-MM-DD.jsonl 에 한 줄씩 덧붙인다 (SQLite 대신, issue #10).
// 읽기·쓰기가 아래 시간을 넘기면 slow 로 경고한다. 경고가 잦아지면 SQLite 로 옮길 때다.
var (
	slowRead  = 300 * time.Millisecond
	slowWrite = 50 * time.Millisecond
)

const timeFmt = "2006-01-02 15:04:05.000"

// TurnRecord 는 LLM 호출 한 번의 기록이다.
type TurnRecord struct {
	Type        string  `json:"type"` // "turn"
	Time        string  `json:"time"`
	Source      string  `json:"source"` // web-chat | web-scenario | cli
	Session     string  `json:"session,omitempty"`
	Scenario    string  `json:"scenario,omitempty"`
	Turn        int     `json:"turn,omitempty"` // 1부터
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	Server      string  `json:"server"`
	Think       string  `json:"think"`
	Effort      string  `json:"reasoning_effort,omitempty"`
	Stream      bool    `json:"stream"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	User        string  `json:"user"`
	Content     string  `json:"content"`
	Reasoning   string  `json:"reasoning,omitempty"`
	Usage       *Usage  `json:"usage,omitempty"`
	TTFTms      int64   `json:"ttft_ms,omitempty"`
	ElapsedMs   int64   `json:"elapsed_ms"`
	Finish      string  `json:"finish,omitempty"`
	Error       string  `json:"error,omitempty"`
}

// CheckResult 는 시나리오 한 턴의 기대 문자열 검사 결과다.
type CheckResult struct {
	Turn    int      `json:"turn"`
	Expect  []string `json:"expect"`
	Missing []string `json:"missing,omitempty"`
}

// ScenarioRecord 는 시나리오 한 번 실행의 요약이다.
type ScenarioRecord struct {
	Type      string        `json:"type"` // "scenario"
	Time      string        `json:"time"`
	Source    string        `json:"source"`
	Session   string        `json:"session,omitempty"`
	Name      string        `json:"name"`
	BaseURL   string        `json:"base_url"`
	Model     string        `json:"model"`
	Server    string        `json:"server"`
	Think     string        `json:"think"` // 강제한 값, 시나리오 값을 따랐으면 "scenario"
	Turns     int           `json:"turns"`
	Done      int           `json:"done"`
	Checks    int           `json:"checks"`
	Fails     int           `json:"fails"`
	Stopped   bool          `json:"stopped,omitempty"`
	ElapsedMs int64         `json:"elapsed_ms"`
	Note      string        `json:"note,omitempty"`
	Results   []CheckResult `json:"results,omitempty"`
}

// runStore 는 기록 폴더다. slow 가 있으면 느린 읽기·쓰기를 알린다.
type runStore struct {
	dir  string
	mu   sync.Mutex
	slow func(msg string)
}

func newRunStore(dir string, slow func(string)) *runStore {
	if slow == nil {
		slow = func(msg string) { fmt.Fprintln(os.Stderr, "[slow] "+msg) }
	}
	return &runStore{dir: dir, slow: slow}
}

// runsDir 는 기록 폴더를 정한다. scenarios 폴더 옆(harness/runs), 없으면 현재 폴더의 runs.
func runsDir() string {
	if dir := findUp("scenarios", true); dir != "" {
		return filepath.Join(filepath.Dir(dir), "runs")
	}
	return "runs"
}

func newSession() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *runStore) append(rec any) error {
	if s == nil {
		return nil
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.dir, time.Now().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if d := time.Since(start); d > slowWrite {
		s.slow(fmt.Sprintf("기록 쓰기 느림: %s, %s (기준 %s)", filepath.Base(path), d.Round(time.Millisecond), slowWrite))
	}
	if werr != nil {
		return werr
	}
	return cerr
}

// load 는 최근 days 일(0 이면 전부)의 기록을 오래된 것부터 돌려준다.
func (s *runStore) load(days int) ([]json.RawMessage, error) {
	start := time.Now()
	files, err := filepath.Glob(filepath.Join(s.dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if days > 0 {
		from := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
		kept := files[:0]
		for _, f := range files {
			if strings.TrimSuffix(filepath.Base(f), ".jsonl") >= from {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	var out []json.RawMessage
	var size int64
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		if st, err := fh.Stat(); err == nil {
			size += st.Size()
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || !json.Valid(line) {
				continue // 쓰다 끊긴 줄은 건너뛴다
			}
			out = append(out, append(json.RawMessage(nil), line...))
		}
		err = sc.Err()
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
	}
	if d := time.Since(start); d > slowRead {
		s.slow(fmt.Sprintf("기록 읽기 느림: 파일 %d개, %d줄, %.1fMB, %s (기준 %s) — 잦으면 SQLite 이전 검토",
			len(files), len(out), float64(size)/1e6, d.Round(time.Millisecond), slowRead))
	}
	return out, nil
}

// turnRecord 는 호출 결과로 기록 한 줄을 만든다.
func turnRecord(source, session string, opt Options, think string, msgs []Message, res Result, err error) TurnRecord {
	rec := TurnRecord{
		Type: "turn", Time: time.Now().Format(timeFmt), Source: source, Session: session,
		BaseURL: opt.Base, Model: opt.Model, Server: opt.Server, Think: think, Effort: opt.Effort,
		Stream: opt.Stream, Temperature: opt.Temp, MaxTokens: opt.MaxTokens,
		Content: res.Content, Reasoning: res.Reasoning, Usage: res.Usage,
		TTFTms: res.TTFT.Milliseconds(), ElapsedMs: res.Elapsed.Milliseconds(), Finish: res.Finish,
	}
	if rec.Think == "" {
		rec.Think = "auto"
	}
	if len(msgs) > 0 {
		rec.User = msgs[len(msgs)-1].Content
	}
	if err != nil {
		rec.Error = err.Error()
	}
	return rec
}
