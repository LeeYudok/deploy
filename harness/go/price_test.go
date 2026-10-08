package main

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// 모델 이름에 / 와 . 이 있어도 저장했다 다시 읽으면 같은 단가가 나온다.
func TestPriceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.toml")
	in := map[string]Price{"RedHatAI/Qwen3.8-27B-INT4": {Input: 0.2, Output: 0.6}, "deepseek-v4-flash-0731": {Input: 0.27, Output: 1.1}, "free": {}}
	if err := saveToml(path, map[string]string{"model": "x"}, []Preset{{ID: "p", BaseURL: "http://h/v1", Model: "m"}}, in); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	got := pricesFrom(cfg)
	if len(got) != 2 || got["RedHatAI/Qwen3.8-27B-INT4"] != in["RedHatAI/Qwen3.8-27B-INT4"] || got["deepseek-v4-flash-0731"] != in["deepseek-v4-flash-0731"] {
		t.Fatalf("got %+v", got)
	}
	if len(presetsFrom(cfg)) != 1 || cfg["llm.model"] != "x" {
		t.Fatalf("preset or llm lost: %v", cfg)
	}
}

func TestPriceCost(t *testing.T) {
	p := Price{Input: 0.5, Output: 2}
	c := p.cost(&Usage{PromptTokens: 2_000_000, CompletionTokens: 500_000})
	if math.Abs(c-2.0) > 1e-9 { // 2M*0.5 + 0.5M*2 = 1 + 1
		t.Fatalf("cost %v, want 2", c)
	}
	if p.cost(nil) != 0 || fmtUSD(0.000123) != "$0.00012" || fmtUSD(12.345) != "$12.35" {
		t.Fatal("format")
	}
}

func TestMatchOpenRouter(t *testing.T) {
	list, err := parseCatalog([]byte(`{"data":[
		{"id":"obsidian/Qwen3.8-27B","name":"custom host","pricing":{"prompt":"0.0000004","completion":"0.00000421"}},
		{"id":"deepseek/deepseek-v4-flash-free","name":"free","pricing":{"request":"0"}},
		{"id":"qwen/qwen3.8-27b","name":"Qwen3.8 27B","pricing":{"prompt":"0.000000425","completion":"0.00000255"}},
		{"id":"qwen/qwen3.8-flash","name":"Qwen3.8 Flash","pricing":{"prompt":"0.00000015","completion":"0.00000047"}},
		{"id":"deepseek/deepseek-v4-flash-0731:batch","name":"batch","pricing":{"prompt":"0.00000001","completion":"0.0000005"}},
		{"id":"deepseek/deepseek-v4-flash-0731","name":"DeepSeek V4 Flash 0731","pricing":{"prompt":"0.000000018","completion":"0.00000128"}},
		{"id":"openrouter/auto","name":"Auto","pricing":{"prompt":"-1","completion":"-1"}}]}`))
	if err != nil || len(list) != 5 {
		t.Fatalf("parse: %v %d", err, len(list))
	}
	cases := map[string]string{
		"RedHatAI/Qwen3.8-27B-INT4": "qwen/qwen3.8-27b", // 조직 접두어·양자화 꼬리표를 떼고 맞춘다
		"Qwen/Qwen3.8-27B-AWQ":      "qwen/qwen3.8-27b",
		"deepseek-v4-flash-0731":    "deepseek/deepseek-v4-flash-0731", // :batch 보다 변형 없는 id
		"qwen3.8:27b":               "qwen/qwen3.8-27b",                // Ollama 이름:태그
	}
	for model, want := range cases {
		m, ok := matchCatalog(model, list)
		if !ok || m.ID != want {
			t.Fatalf("%s: got %q %v, want %q", model, m.ID, ok, want)
		}
	}
	if m, _ := matchCatalog("RedHatAI/Qwen3.8-27B-INT4", list); m.Input != 0.425 || m.Output != 2.55 {
		t.Fatalf("price per 1M: %+v", m)
	}
	if _, ok := matchCatalog("mock-model", list); ok {
		t.Fatal("mock-model should not match")
	}
}

func TestPresetOrder(t *testing.T) {
	cfg := map[string]string{
		"preset.z.label": "z", "preset.z.order": "1",
		"preset.a.label": "a",
		"preset.m.label": "m", "preset.m.order": "2",
	}
	var ids []string
	for _, p := range presetsFrom(cfg) {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "z,m,a" {
		t.Fatalf("order %v, want z,m,a", ids)
	}
}
