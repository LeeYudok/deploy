package main

import (
	"math"
	"path/filepath"
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
