package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLevels(t *testing.T) {
	if got, err := parseLevels(" 1, 2,8 "); err != nil || fmt.Sprint(got) != "[1 2 8]" {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "0", "x", "129"} {
		if _, err := parseLevels(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
}

func TestRecommendLevel(t *testing.T) {
	lv := func(level int, tps float64, errs int) BenchLevel {
		return BenchLevel{Level: level, OK: 1, TokPerSec: tps, Errors: errs}
	}
	cases := []struct {
		in   []BenchLevel
		want int
	}{
		{[]BenchLevel{lv(1, 10, 0), lv(2, 19, 0), lv(4, 35, 0), lv(8, 36, 0)}, 4}, // 8 은 3% 증가 → 포화
		{[]BenchLevel{lv(1, 10, 0), lv(2, 19, 0), lv(4, 40, 1)}, 2},               // 4 에서 오류
		{[]BenchLevel{lv(1, 10, 2)}, 0},
	}
	for i, c := range cases {
		if got := recommendLevel(c.in); got != c.want {
			t.Fatalf("case %d: got %d, want %d", i, got, c.want)
		}
	}
}

// 동시에 2건까지만 처리하는 가짜 서버로 수준별 지표를 확인한다.
func TestRunBench(t *testing.T) {
	var inflight, peak int32
	slots := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slots <- struct{}{}
		defer func() { <-slots }()
		n := atomic.AddInt32(&inflight, 1)
		defer atomic.AddInt32(&inflight, -1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":10}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	opt := Options{Client: srv.Client(), Base: srv.URL, Model: "m", Server: ServerSGLang}
	var samples int32
	res, err := runBench(context.Background(), opt, BenchConfig{Levels: []int{1, 4}, Requests: 4, Prompt: "p"},
		func(BenchSample) { atomic.AddInt32(&samples, 1) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || samples != 8 {
		t.Fatalf("levels=%d samples=%d", len(res), samples)
	}
	for _, b := range res {
		if b.OK != 4 || b.Errors != 0 || b.OutTotal != 40 || b.TokPerSec <= 0 || b.LatP95ms < 30 {
			t.Fatalf("bad level %+v", b)
		}
	}
	if peak != 2 {
		t.Fatalf("server saw %d concurrent requests, want 2 (slots)", peak)
	}
	// 동시 4 는 슬롯 2개에 막혀 큐에서 기다리므로 p95 지연이 동시 1 보다 길다
	if res[1].LatP95ms <= res[0].LatP95ms {
		t.Fatalf("p95 at 4 (%d) should exceed p95 at 1 (%d)", res[1].LatP95ms, res[0].LatP95ms)
	}
}
