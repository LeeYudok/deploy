package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Price 는 모델 하나의 단가다. 100만 토큰당 USD (issue #18).
// 온프렘 모델은 실제 청구액이 없으므로, 상용 API 단가로 환산해 비교하는 용도다.
type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
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
