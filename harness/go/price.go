package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Price 는 모델 하나의 단가다. 100만 토큰당 USD (issue #18).
// 온프렘 모델은 실제 청구액이 없으므로, 상용 API 단가로 환산해 비교하는 용도다.
type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Source string  `json:"source,omitempty"` // 단가를 가져온 곳 (예: openrouter:qwen/qwen3.8-27b)
}

func (p Price) set() bool { return p.Input > 0 || p.Output > 0 }

// cost 는 usage 의 환산 비용(USD)이다. 추론 토큰은 completion 에 들어 있으므로 출력 단가로 센다.
func (p Price) cost(u *Usage) float64 {
	if u == nil {
		return 0
	}
	return float64(u.PromptTokens)/1e6*p.Input + float64(u.CompletionTokens)/1e6*p.Output
}

// pricesFrom 은 설정의 [price."<모델>"] 섹션(input, output)을 읽는다.
func pricesFrom(cfg map[string]string) map[string]Price {
	out := map[string]Price{}
	for k, v := range cfg {
		rest, ok := strings.CutPrefix(k, "price.")
		if !ok {
			continue
		}
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		model, field := rest[:dot], rest[dot+1:]
		if uq, err := strconv.Unquote(model); err == nil {
			model = uq
		}
		if field == "source" {
			p := out[model]
			p.Source = v
			out[model] = p
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			continue
		}
		p := out[model]
		switch field {
		case "input":
			p.Input = f
		case "output":
			p.Output = f
		default:
			continue
		}
		out[model] = p
	}
	return out
}

// writePrices 는 단가 섹션을 TOML 로 쓴다. 모델 이름에 / 나 . 이 있어 따옴표로 감싼다.
func writePrices(b *strings.Builder, prices map[string]Price) {
	models := make([]string, 0, len(prices))
	for m, p := range prices {
		if p.set() {
			models = append(models, m)
		}
	}
	sort.Strings(models)
	for _, m := range models {
		p := prices[m]
		fmt.Fprintf(b, "\n# 100만 토큰당 USD\n[price.%s]\ninput = %q\noutput = %q\n", strconv.Quote(m),
			strconv.FormatFloat(p.Input, 'f', -1, 64), strconv.FormatFloat(p.Output, 'f', -1, 64))
		if p.Source != "" {
			fmt.Fprintf(b, "source = %q\n", p.Source)
		}
	}
}

func fmtUSD(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.01:
		return fmt.Sprintf("$%.5f", v)
	case v < 1:
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// ---- OpenRouter 단가 ----

const openRouterModels = "https://openrouter.ai/api/v1/models"

// ORModel 은 OpenRouter 모델 하나의 단가다 (100만 토큰당 USD 로 바꾼 값).
type ORModel struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// parseOpenRouter 는 /api/v1/models 응답을 읽는다. pricing 은 토큰당 USD 문자열이다.
func parseOpenRouter(body []byte) ([]ORModel, error) {
	var raw struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	var out []ORModel
	for _, m := range raw.Data {
		in, err1 := strconv.ParseFloat(m.Pricing.Prompt, 64)
		o, err2 := strconv.ParseFloat(m.Pricing.Completion, 64)
		if err1 != nil || err2 != nil || in < 0 || o < 0 {
			continue // 가변 단가(-1) 등은 건너뛴다
		}
		out = append(out, ORModel{ID: m.ID, Name: m.Name, Input: round6(in * 1e6), Output: round6(o * 1e6)})
	}
	return out, nil
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// quantSuffix 는 양자화·배포 형식 꼬리표다. 같은 모델의 양자화본은 원본 단가로 본다.
var quantSuffix = regexp.MustCompile(`[-_.](int4|int8|w4a16|w8a8|awq|gptq|gguf|fp8|fp4|nvfp4|mlx|bnb|q4_k_m|q8_0|4bit|8bit)$`)

// normModel 은 비교용 모델 이름이다: 소문자, 조직 접두어·변형(:batch 등)·양자화 꼬리표 제거.
func normModel(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	s = strings.TrimPrefix(s, "~")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	for {
		t := quantSuffix.ReplaceAllString(s, "")
		if t == s {
			return s
		}
		s = t
	}
}

// matchOpenRouter 는 하네스 모델 이름과 같은 OpenRouter 모델을 찾는다. 정규화한 이름이 정확히 같아야 한다.
// 여러 개면 변형 없는 id(:batch 등이 없는 것)를 고른다.
func matchOpenRouter(model string, list []ORModel) (ORModel, bool) {
	want := normModel(model)
	var best ORModel
	found := false
	for _, m := range list {
		if normModel(m.ID) != want {
			continue
		}
		plain := !strings.Contains(m.ID, ":") && !strings.HasPrefix(m.ID, "~")
		if !found || plain {
			best, found = m, true
			if plain {
				break
			}
		}
	}
	return best, found
}
