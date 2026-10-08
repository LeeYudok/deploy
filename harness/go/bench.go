package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 동시 처리 부하 테스트 (issue #17). 동시 수준마다 요청을 한꺼번에 보내 처리량과 지연을 잰다.

const maxBenchLevel = 128

// BenchConfig 는 부하 테스트 설정이다.
type BenchConfig struct {
	Levels   []int  `json:"levels"`   // 동시 수준 (예: 1,2,4,8)
	Requests int    `json:"requests"` // 수준당 요청 수. 0 이면 수준의 2배
	Prompt   string `json:"prompt"`
	System   string `json:"system"`
	Think    string `json:"think"`
}

// BenchSample 은 요청 한 건의 결과다.
type BenchSample struct {
	Level      int    `json:"level"`
	Index      int    `json:"index"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	TTFTms     int64  `json:"ttft_ms"`
	LatencyMs  int64  `json:"latency_ms"`
	Completion int    `json:"completion_tokens"`
	Prompt     int    `json:"prompt_tokens"`
}

// BenchLevel 은 동시 수준 하나의 요약이다.
type BenchLevel struct {
	Level       int     `json:"level"`
	Requests    int     `json:"requests"`
	OK          int     `json:"ok"`
	Errors      int     `json:"errors"`
	WallMs      int64   `json:"wall_ms"`
	RPS         float64 `json:"rps"`           // 초당 끝난 요청 수
	TokPerSec   float64 `json:"tok_per_sec"`   // 전체 출력 토큰 처리량
	ReqTokPerS  float64 `json:"req_tok_per_s"` // 요청 하나가 받는 출력 속도 (평균)
	LatP50ms    int64   `json:"lat_p50_ms"`
	LatP95ms    int64   `json:"lat_p95_ms"`
	LatMaxMs    int64   `json:"lat_max_ms"`
	TTFTP50ms   int64   `json:"ttft_p50_ms"`
	TTFTP95ms   int64   `json:"ttft_p95_ms"`
	AvgTokens   float64 `json:"avg_completion_tokens"`
	PromptTotal int     `json:"prompt_tokens"`
	OutTotal    int     `json:"completion_tokens"`
	FirstError  string  `json:"first_error,omitempty"`
}

// parseLevels 는 "1,2,4,8" 을 읽는다.
func parseLevels(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > maxBenchLevel {
			return nil, fmt.Errorf("동시 수준은 1~%d 의 정수: %q", maxBenchLevel, p)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("동시 수준을 하나 이상 적으세요 (예: 1,2,4,8)")
	}
	return out, nil
}

func percentile(xs []int64, p float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := int(float64(len(s)-1)*p + 0.5)
	return s[idx]
}

// runBench 는 수준마다 요청을 동시에 보내고, 요청이 끝날 때마다 onSample, 수준이 끝날 때마다 onLevel 을 부른다.
// 요청은 스트리밍으로 보내 TTFT 를 잰다. 프롬프트 끝에 번호를 붙여 응답 캐시 효과를 줄인다.
func runBench(ctx context.Context, opt Options, cfg BenchConfig, onSample func(BenchSample), onLevel func(BenchLevel)) ([]BenchLevel, error) {
	opt.Stream = true
	opt.Trace = nil // 요청마다 로그를 남기면 로그가 넘친다. 수준 요약만 남긴다
	var levels []BenchLevel
	for _, level := range cfg.Levels {
		n := cfg.Requests
		if n <= 0 {
			n = level * 2
		}
		if n < level {
			n = level // 모든 작업자가 한 번은 일하도록
		}
		jobs := make(chan int)
		samples := make([]BenchSample, n)
		var mu sync.Mutex
		var wg sync.WaitGroup
		start := time.Now()
		for w := 0; w < level; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					msgs := []Message{}
					if cfg.System != "" {
						msgs = append(msgs, Message{Role: "system", Content: cfg.System})
					}
					msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf("%s\n(요청 #%d-%d)", cfg.Prompt, level, i+1)})
					r, err := complete(ctx, opt, msgs, cfg.Think, func(string, string) {})
					s := BenchSample{Level: level, Index: i + 1, OK: err == nil, TTFTms: r.TTFT.Milliseconds(), LatencyMs: r.Elapsed.Milliseconds()}
					if err != nil {
						s.Error = oneLine(err.Error(), 300)
					}
					if r.Usage != nil {
						s.Completion, s.Prompt = r.Usage.CompletionTokens, r.Usage.PromptTokens
					}
					mu.Lock()
					samples[i] = s
					if onSample != nil {
						onSample(s)
					}
					mu.Unlock()
				}
			}()
		}
	feed:
		for i := 0; i < n; i++ {
			select {
			case jobs <- i:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()
		if ctx.Err() != nil {
			return levels, ctx.Err()
		}
		levels = append(levels, summarizeLevel(level, samples, time.Since(start)))
		if onLevel != nil {
			onLevel(levels[len(levels)-1])
		}
	}
	return levels, nil
}

func summarizeLevel(level int, samples []BenchSample, wall time.Duration) BenchLevel {
	b := BenchLevel{Level: level, Requests: len(samples), WallMs: wall.Milliseconds()}
	var lat, ttft []int64
	var reqRate float64
	for _, s := range samples {
		if !s.OK {
			b.Errors++
			if b.FirstError == "" {
				b.FirstError = s.Error
			}
			continue
		}
		b.OK++
		lat = append(lat, s.LatencyMs)
		if s.TTFTms > 0 {
			ttft = append(ttft, s.TTFTms)
		}
		b.OutTotal += s.Completion
		b.PromptTotal += s.Prompt
		// 요청 하나의 출력 속도: 첫 토큰 이후 시간으로 나눈다
		if gen := s.LatencyMs - s.TTFTms; gen > 0 && s.Completion > 0 {
			reqRate += float64(s.Completion) / (float64(gen) / 1000)
		}
	}
	if secs := wall.Seconds(); secs > 0 {
		b.RPS = float64(b.OK) / secs
		b.TokPerSec = float64(b.OutTotal) / secs
	}
	if b.OK > 0 {
		b.AvgTokens = float64(b.OutTotal) / float64(b.OK)
		b.ReqTokPerS = reqRate / float64(b.OK)
	}
	b.LatP50ms, b.LatP95ms, b.LatMaxMs = percentile(lat, .5), percentile(lat, .95), percentile(lat, 1)
	b.TTFTP50ms, b.TTFTP95ms = percentile(ttft, .5), percentile(ttft, .95)
	return b
}

// recommendLevel 은 권장 동시 수를 고른다: 오류가 없고, 처리량이 이전 수준보다 10% 이상 늘어난 마지막 수준.
// 처리량이 10% 미만으로 늘면 그 앞이 포화 지점이다. 오류가 난 수준부터는 보지 않는다. 고를 수 없으면 0.
func recommendLevel(levels []BenchLevel) int {
	best := 0
	var prev float64
	for i, b := range levels {
		if b.Errors > 0 || b.OK == 0 {
			break
		}
		if i == 0 || b.TokPerSec >= prev*1.1 {
			best = b.Level
			prev = b.TokPerSec
			continue
		}
		break
	}
	return best
}
