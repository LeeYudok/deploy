// sglang(OpenAI 호환) LLM 호출 테스트 클라이언트
//
// 사용법:
//
//	go run . -p "안녕하세요"
//	go run . -stream -p "Go 언어 장점 3가지"
//	go run . -models
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

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	baseURL := flag.String("url", getenv("LLM_BASE_URL", "http://192.168.157.153:30112/v1"), "API base URL")
	model := flag.String("model", getenv("LLM_MODEL", "deepseek-v4-flash-0731"), "모델명")
	apiKey := flag.String("key", getenv("LLM_API_KEY", ""), "API 키 (필요한 경우)")
	prompt := flag.String("p", "안녕하세요. 간단히 자기소개 해주세요.", "사용자 프롬프트")
	system := flag.String("sys", "You are a helpful assistant. 한국어로 답변하세요.", "시스템 프롬프트")
	temp := flag.Float64("t", 0.7, "temperature")
	maxTokens := flag.Int("max", 1024, "max_tokens")
	stream := flag.Bool("stream", false, "스트리밍 응답")
	listModels := flag.Bool("models", false, "모델 목록만 조회")
	timeout := flag.Duration("timeout", 300*time.Second, "요청 타임아웃")
	flag.Parse()

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
