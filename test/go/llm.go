package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model              string          `json:"model"`
	Messages           []Message       `json:"messages"`
	Temperature        float64         `json:"temperature"`
	MaxTokens          int             `json:"max_tokens,omitempty"`
	Stream             bool            `json:"stream"`
	StreamOptions      map[string]bool `json:"stream_options,omitempty"`
	ChatTemplateKwargs map[string]bool `json:"chat_template_kwargs,omitempty"`
	ReasoningEffort    string          `json:"reasoning_effort,omitempty"`
}

type Usage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// Result 는 한 번의 chat/completions 호출 결과다.
type Result struct {
	Content   string // <think> 블록을 뺀 답변 (멀티턴 히스토리에 넣는 값)
	Reasoning string // reasoning_content 또는 content 안의 <think> 블록
	Finish    string
	Usage     *Usage
	TTFT      time.Duration // 스트리밍일 때 첫 토큰까지 걸린 시간
	Elapsed   time.Duration
}

// Options 는 호출마다 공통으로 쓰는 설정이다.
type Options struct {
	Client    *http.Client
	Base      string
	APIKey    string
	Model     string
	Temp      float64
	MaxTokens int
	Stream    bool
	Effort    string
}

// Sink 는 응답 조각을 받는다. kind 는 "reasoning" 또는 "content".
type Sink func(kind, text string)

// Scenario 는 멀티턴 시나리오 파일(JSON) 형식이다.
type Scenario struct {
	Name   string  `json:"name"`
	System *string `json:"system,omitempty"` // 없으면 -sys 값
	Think  string  `json:"think,omitempty"`  // 시나리오 기본 thinking (on/off/auto)
	Turns  []Turn  `json:"turns"`
}

type Turn struct {
	User   string   `json:"user"`
	Think  string   `json:"think,omitempty"`  // 이 턴만 thinking 변경
	Expect []string `json:"expect,omitempty"` // 답변에 모두 들어 있어야 함. "a|b" 는 둘 중 하나
}

// configKeys 는 .env.toml [llm] 섹션에 저장하는 키 순서다.
var configKeys = []string{"base_url", "model", "api_key", "think", "reasoning_effort", "temperature", "max_tokens", "system"}

// loadToml 은 key = "value" 형태의 단순 TOML 을 읽는다.
// [section] 이 있으면 키는 "section.key" 로 저장된다.
func loadToml(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := map[string]string{}
	section := ""
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: '=' 없음", path, n)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			end := strings.LastIndex(v, `"`)
			if end == 0 {
				return nil, fmt.Errorf("%s:%d: 닫는 따옴표 없음", path, n)
			}
			if v, err = strconv.Unquote(v[:end+1]); err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, n, err)
			}
		} else if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if section != "" {
			k = section + "." + k
		}
		cfg[k] = v
	}
	return cfg, sc.Err()
}

// saveToml 은 [llm] 섹션 값을 .env.toml 로 쓴다. 기존 주석은 남지 않는다.
func saveToml(path string, llm map[string]string) error {
	var b strings.Builder
	b.WriteString("# LLM 접속 설정 (웹 UI 에서 저장함)\n[llm]\n")
	written := map[string]bool{}
	write := func(k string) {
		if v, ok := llm[k]; ok && !written[k] {
			fmt.Fprintf(&b, "%s = %s\n", k, strconv.Quote(v))
			written[k] = true
		}
	}
	for _, k := range configKeys {
		write(k)
	}
	rest := make([]string, 0, len(llm))
	for k := range llm {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		write(k)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600) // 이미 있던 파일도 API 키가 들어 있으니 본인만 읽게 한다
}

// findUp 은 현재 디렉터리와 실행파일 디렉터리에서 시작해 상위 2단계까지 name 을 찾는다.
// test/go/bin/llmtest.exe 로 실행해도 test/.env.toml, test/scenarios 를 찾는다.
func findUp(name string, wantDir bool) string {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, dir := range starts {
		for i := 0; i < 3; i++ {
			c := filepath.Join(dir, name)
			if st, err := os.Stat(c); err == nil && st.IsDir() == wantDir {
				return c
			}
			dir = filepath.Dir(dir)
		}
	}
	return ""
}

// thinkKwargs 는 thinking 모드를 chat_template_kwargs 로 바꾼다.
// 모델마다 템플릿 변수 이름이 달라서 둘 다 보낸다 (DeepSeek: thinking, Qwen3/GLM: enable_thinking).
// 템플릿이 쓰지 않는 변수는 무시된다.
func thinkKwargs(mode string) (map[string]bool, error) {
	switch strings.ToLower(mode) {
	case "", "auto":
		return nil, nil
	case "on", "true", "1":
		return map[string]bool{"thinking": true, "enable_thinking": true}, nil
	case "off", "false", "0":
		return map[string]bool{"thinking": false, "enable_thinking": false}, nil
	}
	return nil, fmt.Errorf("think 값은 on / off / auto 중 하나: %q", mode)
}

func newRequest(ctx context.Context, method, url, apiKey string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return req, nil
}

func do(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return resp, nil
}

type ModelInfo struct {
	ID          string `json:"id"`
	OwnedBy     string `json:"owned_by"`
	MaxModelLen int    `json:"max_model_len"`
}

func listModels(ctx context.Context, opt Options) ([]ModelInfo, error) {
	req, err := newRequest(ctx, http.MethodGet, opt.Base+"/models", opt.APIKey, nil)
	if err != nil {
		return nil, err
	}
	resp, err := do(opt.Client, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// complete 는 messages 로 한 번 호출하고 응답 조각을 sink 로 넘긴다.
func complete(ctx context.Context, opt Options, msgs []Message, think string, sink Sink) (Result, error) {
	kw, err := thinkKwargs(think)
	if err != nil {
		return Result{}, err
	}
	r := ChatRequest{
		Model:              opt.Model,
		Messages:           msgs,
		Temperature:        opt.Temp,
		MaxTokens:          opt.MaxTokens,
		Stream:             opt.Stream,
		ChatTemplateKwargs: kw,
		ReasoningEffort:    opt.Effort,
	}
	if opt.Stream {
		r.StreamOptions = map[string]bool{"include_usage": true}
	}
	body, _ := json.Marshal(r)
	req, err := newRequest(ctx, http.MethodPost, opt.Base+"/chat/completions", opt.APIKey, body)
	if err != nil {
		return Result{}, err
	}
	if opt.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	start := time.Now()
	resp, err := do(opt.Client, req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	var res Result
	if opt.Stream {
		res, err = readStream(resp.Body, sink, start)
	} else {
		res, err = readJSON(resp.Body, sink)
	}
	res.Elapsed = time.Since(start)
	return res, err
}

func readJSON(body io.Reader, sink Sink) (Result, error) {
	var out ChatResponse
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		return Result{}, err
	}
	if len(out.Choices) == 0 {
		return Result{}, fmt.Errorf("응답에 choices 없음")
	}
	c := out.Choices[0]
	reasoning, content := splitThink(c.Message.Content)
	if c.Message.ReasoningContent != "" {
		reasoning = strings.TrimSpace(c.Message.ReasoningContent)
	}
	if reasoning != "" {
		sink("reasoning", reasoning)
	}
	sink("content", content)
	return Result{Content: content, Reasoning: reasoning, Finish: c.FinishReason, Usage: out.Usage}, nil
}

func readStream(body io.Reader, sink Sink, start time.Time) (Result, error) {
	var res Result
	var content, reasoning strings.Builder
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk ChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			res.Usage = chunk.Usage
		}
		for _, c := range chunk.Choices {
			if res.TTFT == 0 && (c.Delta.Content != "" || c.Delta.ReasoningContent != "") {
				res.TTFT = time.Since(start)
			}
			if c.Delta.ReasoningContent != "" {
				reasoning.WriteString(c.Delta.ReasoningContent)
				sink("reasoning", c.Delta.ReasoningContent)
			}
			if c.Delta.Content != "" {
				content.WriteString(c.Delta.Content)
				sink("content", c.Delta.Content)
			}
			if c.FinishReason != "" {
				res.Finish = c.FinishReason
			}
		}
	}
	// reasoning parser 가 없는 서버는 <think> 를 content 에 섞어 보낸다.
	tagged, answer := splitThink(content.String())
	res.Content = answer
	res.Reasoning = strings.TrimSpace(reasoning.String() + tagged)
	return res, sc.Err()
}

// splitThink 는 content 안의 <think>...</think> 블록을 떼어 낸다.
// 여는 태그 없이 </think> 만 있는 경우(템플릿이 <think> 를 프롬프트에 넣은 경우)도 처리한다.
func splitThink(s string) (reasoning, content string) {
	reasoning, content, ok := strings.Cut(s, "</think>")
	if !ok {
		return "", strings.TrimSpace(s)
	}
	if _, after, found := strings.Cut(reasoning, "<think>"); found {
		reasoning = after
	}
	return strings.TrimSpace(reasoning), strings.TrimSpace(content)
}

// checkExpect 는 답변에 기대 문자열이 모두 있는지 본다 (대소문자 무시, "a|b" 는 둘 중 하나).
func checkExpect(answer string, expect []string) (missing []string) {
	low := strings.ToLower(answer)
	for _, e := range expect {
		hit := false
		for _, alt := range strings.Split(e, "|") {
			if strings.Contains(low, strings.ToLower(strings.TrimSpace(alt))) {
				hit = true
				break
			}
		}
		if !hit {
			missing = append(missing, e)
		}
	}
	return missing
}

// scenarioFiles 는 쉼표로 구분한 파일/폴더 목록을 JSON 파일 목록으로 펼친다.
func scenarioFiles(spec string) ([]string, error) {
	var files []string
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			files = append(files, p)
			continue
		}
		m, err := filepath.Glob(filepath.Join(p, "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(m)
		files = append(files, m...)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("시나리오 파일 없음: %s", spec)
	}
	return files, nil
}

func loadScenario(path string) (Scenario, error) {
	var sc Scenario
	b, err := os.ReadFile(path)
	if err != nil {
		return sc, err
	}
	if err := json.Unmarshal(b, &sc); err != nil {
		return sc, fmt.Errorf("%s: %v", path, err)
	}
	if sc.Name == "" {
		sc.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return sc, nil
}
