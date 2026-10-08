// sglang(OpenAI 호환) LLM 호출 테스트 클라이언트
//
// 사용법:
//
//	go run . -p "안녕하세요"
//	go run . -stream -p "Go 언어 장점 3가지"
//	go run . -models
//
// 접속 정보는 .env.toml 에서 읽는다 (.env.toml.example 참고).
// 우선순위: 명령행 플래그 > 환경변수 > .env.toml > 기본값
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
		Delta   struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

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
		line := strings.TrimSpace(sc.Text())
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

// findConfig 는 현재 디렉터리, 실행파일 디렉터리 순으로 .env.toml 을 찾는다.
func findConfig() string {
	candidates := []string{".env.toml"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env.toml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func main() {
	cfgPath := findConfig()
	for i, a := range os.Args[1:] {
		if a == "-config" || a == "--config" {
			if i+2 < len(os.Args) {
				cfgPath = os.Args[i+2]
			}
		} else if v, ok := strings.CutPrefix(strings.TrimLeft(a, "-"), "config="); ok {
			cfgPath = v
		}
	}
	cfg := map[string]string{}
	if cfgPath != "" {
		var err error
		if cfg, err = loadToml(cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR: 설정파일:", err)
			os.Exit(1)
		}
	}
	conf := func(env, key, def string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		if v, ok := cfg["llm."+key]; ok {
			return v
		}
		if v, ok := cfg[key]; ok {
			return v
		}
		return def
	}

	flag.String("config", cfgPath, "설정파일 경로")
	baseURL := flag.String("url", conf("LLM_BASE_URL", "base_url", ""), "API base URL")
	model := flag.String("model", conf("LLM_MODEL", "model", ""), "모델명")
	apiKey := flag.String("key", conf("LLM_API_KEY", "api_key", ""), "API 키 (필요한 경우)")
	prompt := flag.String("p", "안녕하세요. 간단히 자기소개 해주세요.", "사용자 프롬프트")
	system := flag.String("sys", "You are a helpful assistant. 한국어로 답변하세요.", "시스템 프롬프트")
	temp := flag.Float64("t", 0.7, "temperature")
	maxTokens := flag.Int("max", 1024, "max_tokens")
	stream := flag.Bool("stream", false, "스트리밍 응답")
	listModels := flag.Bool("models", false, "모델 목록만 조회")
	timeout := flag.Duration("timeout", 300*time.Second, "요청 타임아웃")
	flag.Parse()

	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "ERROR: 접속주소 없음. .env.toml 의 base_url 을 설정하세요 (.env.toml.example 참고)")
		os.Exit(1)
	}
	if *model == "" && !*listModels {
		fmt.Fprintln(os.Stderr, "ERROR: 모델명 없음. .env.toml 의 model 을 설정하세요")
		os.Exit(1)
	}

	client := &http.Client{Timeout: *timeout}
	base := strings.TrimRight(*baseURL, "/")

	if *listModels {
		if err := getModels(client, base, *apiKey); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
		return
	}

	req := ChatRequest{
		Model: *model,
		Messages: []Message{
			{Role: "system", Content: *system},
			{Role: "user", Content: *prompt},
		},
		Temperature: *temp,
		MaxTokens:   *maxTokens,
		Stream:      *stream,
	}

	start := time.Now()
	var err error
	if *stream {
		err = chatStream(client, base, *apiKey, req)
	} else {
		err = chat(client, base, *apiKey, req)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n[소요시간 %s]\n", time.Since(start).Round(time.Millisecond))
}

func newRequest(method, url, apiKey string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
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

func getModels(client *http.Client, base, apiKey string) error {
	req, err := newRequest(http.MethodGet, base+"/models", apiKey, nil)
	if err != nil {
		return err
	}
	resp, err := do(client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out struct {
		Data []struct {
			ID          string `json:"id"`
			OwnedBy     string `json:"owned_by"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	for _, m := range out.Data {
		fmt.Printf("%s\t(owned_by=%s, max_model_len=%d)\n", m.ID, m.OwnedBy, m.MaxModelLen)
	}
	return nil
}

func chat(client *http.Client, base, apiKey string, r ChatRequest) error {
	body, _ := json.Marshal(r)
	req, err := newRequest(http.MethodPost, base+"/chat/completions", apiKey, body)
	if err != nil {
		return err
	}
	resp, err := do(client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if len(out.Choices) == 0 {
		return fmt.Errorf("응답에 choices 없음")
	}
	fmt.Println(out.Choices[0].Message.Content)
	if out.Usage != nil {
		fmt.Fprintf(os.Stderr, "\n[토큰 prompt=%d completion=%d total=%d]",
			out.Usage.PromptTokens, out.Usage.CompletionTokens, out.Usage.TotalTokens)
	}
	return nil
}

func chatStream(client *http.Client, base, apiKey string, r ChatRequest) error {
	body, _ := json.Marshal(r)
	req, err := newRequest(http.MethodPost, base+"/chat/completions", apiKey, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := do(client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk ChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		for _, c := range chunk.Choices {
			if c.Delta.ReasoningContent != "" {
				fmt.Fprint(os.Stderr, c.Delta.ReasoningContent) // 추론 과정은 stderr로
			}
			fmt.Print(c.Delta.Content)
		}
	}
	fmt.Println()
	return sc.Err()
}
