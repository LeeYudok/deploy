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
			ReasoningContent string `json:"reasoning_content"` // sglang, 구 vLLM
			Reasoning        string `json:"reasoning"`         // Ollama, 신 vLLM
		} `json:"message"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"` // sglang, 구 vLLM
			Reasoning        string `json:"reasoning"`         // Ollama, 신 vLLM
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
	Server    string // 요청을 맞춘 서버 종류
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
	Server    string // sglang / vllm / ollama / openai (auto 는 호출 전에 detectServer 로 정한다)
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
var configKeys = []string{"base_url", "model", "api_key", "server", "think", "reasoning_effort", "temperature", "max_tokens", "system"}

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
// harness/go/bin/llmtest.exe 로 실행해도 harness/.env.toml, harness/scenarios 를 찾는다.
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

// 서버 종류. thinking 을 켜고 끄는 요청 필드가 서버마다 다르다.
const (
	ServerAuto   = "auto"
	ServerSGLang = "sglang"
	ServerVLLM   = "vllm"
	ServerOllama = "ollama"
	ServerOpenAI = "openai" // 그 밖의 OpenAI 호환 서버
)

func normalizeServer(s string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "":
		return ServerAuto, nil
	case ServerAuto, ServerSGLang, ServerVLLM, ServerOllama, ServerOpenAI:
		return v, nil
	}
	return "", fmt.Errorf("server 값은 auto / sglang / vllm / ollama / openai 중 하나: %q", s)
}

// serverFromOwner 는 /v1/models 의 owned_by 로 서버 종류를 정한다.
// sglang 은 "sglang", vLLM 은 "vllm", Ollama 는 "library"(또는 네임스페이스) 를 준다.
func serverFromOwner(owner string) string {
	switch strings.ToLower(owner) {
	case "sglang":
		return ServerSGLang
	case "vllm":
		return ServerVLLM
	case "library", "ollama":
		return ServerOllama
	}
	return ServerOpenAI
}

// detectServer 는 /v1/models 를 불러 서버 종류를 정한다. 실패하면 openai 로 본다.
func detectServer(ctx context.Context, opt Options) (server, owner string, err error) {
	list, err := listModels(ctx, opt)
	if err != nil {
		return ServerOpenAI, "", err
	}
	for _, m := range list {
		if m.ID == opt.Model || owner == "" {
			owner = m.OwnedBy
		}
	}
	return serverFromOwner(owner), owner, nil
}

// parseThink 는 thinking 모드 문자열을 읽는다. set 이 false 면 서버 기본값에 맡긴다.
func parseThink(mode string) (on, set bool, err error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return false, false, nil
	case "on", "true", "1":
		return true, true, nil
	case "off", "false", "0":
		return false, true, nil
	}
	return false, false, fmt.Errorf("think 값은 on / off / auto 중 하나: %q", mode)
}

// thinkParams 는 thinking 모드를 서버에 맞는 요청 필드로 바꾼다.
//
//   - sglang·vLLM·그 밖: chat_template_kwargs 에 thinking(DeepSeek·Kimi)과 enable_thinking(Qwen3·GLM)을
//     함께 보낸다. sglang 도 reasoning_effort=none 을 받으면 이 두 키를 false 로 채운다.
//     템플릿이 쓰지 않는 변수는 무시된다.
//   - Ollama: OpenAI 호환 API 가 chat_template_kwargs 를 무시하므로 끌 때 reasoning_effort=none 을 보낸다.
//
// reasoning_effort 를 사용자가 정했으면 그 값을 그대로 둔다.
func thinkParams(server, mode, effort string) (map[string]bool, string, error) {
	on, set, err := parseThink(mode)
	if err != nil || !set {
		return nil, effort, err
	}
	if server == ServerOllama {
		if !on && effort == "" {
			effort = "none"
		}
		return nil, effort, nil
	}
	return map[string]bool{"thinking": on, "enable_thinking": on}, effort, nil
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
	kw, effort, err := thinkParams(opt.Server, think, opt.Effort)
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
		ReasoningEffort:    effort,
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
	res.Server = opt.Server
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
	if rc := c.Message.ReasoningContent + c.Message.Reasoning; rc != "" {
		reasoning = strings.TrimSpace(rc)
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
			rc := c.Delta.ReasoningContent + c.Delta.Reasoning
			if res.TTFT == 0 && (c.Delta.Content != "" || rc != "") {
				res.TTFT = time.Since(start)
			}
			if rc != "" {
				reasoning.WriteString(rc)
				sink("reasoning", rc)
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
