// sglang(OpenAI 호환) LLM 호출 테스트 클라이언트
//
// 사용법:
//
//	go run . -p "안녕하세요"
//	go run . -stream -think off -p "Go 언어 장점 3가지"
//	go run . -chat                          # 대화형 멀티턴
//	go run . -scenario ../scenarios          # 시나리오 멀티턴 자동 검증
//	go run . -web                            # 웹 UI (설정 변경·대화·시나리오)
//	go run . -models
//
// 접속 정보는 .env.toml 에서 읽는다 (../.env.toml.example 참고).
// 우선순위: 명령행 플래그 > 환경변수 > .env.toml > 기본값
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSystem = "You are a helpful assistant. 한국어로 답변하세요."
	defaultAddr   = "127.0.0.1:8787"
)

func main() {
	cfgPath := findUp(".env.toml", false)
	for i, a := range os.Args[1:] {
		if a == "-config" || a == "--config" {
			if i+2 < len(os.Args) {
				cfgPath = os.Args[i+2]
			}
		} else if v, ok := strings.CutPrefix(strings.TrimLeft(a, "-"), "config="); ok {
			cfgPath = v
		}
	}
	presetID := ""
	for i, a := range os.Args[1:] {
		if a == "-preset" || a == "--preset" {
			if i+2 < len(os.Args) {
				presetID = os.Args[i+2]
			}
		} else if v, ok := strings.CutPrefix(strings.TrimLeft(a, "-"), "preset="); ok {
			presetID = v
		}
	}
	cfg := map[string]string{}
	if cfgPath != "" {
		var err error
		if cfg, err = loadToml(cfgPath); err != nil {
			fail("설정파일: %v", err)
		}
	}
	// 우선순위: 플래그 > -preset > 환경변수 > [llm]
	conf := func(env, key, def string) string {
		if presetID != "" {
			if v := cfg["preset."+presetID+"."+key]; v != "" {
				return v
			}
		}
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
	confTemp, err := strconv.ParseFloat(conf("LLM_TEMPERATURE", "temperature", "0.7"), 64)
	if err != nil {
		fail("temperature 값이 숫자가 아님: %v", err)
	}
	confMax, err := strconv.Atoi(conf("LLM_MAX_TOKENS", "max_tokens", "1024"))
	if err != nil {
		fail("max_tokens 값이 정수가 아님: %v", err)
	}

	flag.String("config", cfgPath, "설정파일 경로")
	flag.String("preset", presetID, ".env.toml 의 [preset.<id>] 로 접속 (예: -preset dev)")
	baseURL := flag.String("url", conf("LLM_BASE_URL", "base_url", ""), "API base URL")
	model := flag.String("model", conf("LLM_MODEL", "model", ""), "모델명")
	apiKey := flag.String("key", conf("LLM_API_KEY", "api_key", ""), "API 키 (필요한 경우)")
	prompt := flag.String("p", "안녕하세요. 간단히 자기소개 해주세요.", "사용자 프롬프트")
	system := flag.String("sys", conf("LLM_SYSTEM", "system", defaultSystem), "시스템 프롬프트")
	temp := flag.Float64("t", confTemp, "temperature")
	maxTokens := flag.Int("max", confMax, "max_tokens")
	stream := flag.Bool("stream", false, "스트리밍 응답")
	models := flag.Bool("models", false, "모델 목록만 조회")
	timeout := flag.Duration("timeout", 300*time.Second, "요청 타임아웃")
	server := flag.String("server", conf("LLM_SERVER", "server", "auto"), "서버 종류: auto(/v1/models 로 판별) / sglang / vllm / ollama / openai")
	think := flag.String("think", conf("LLM_THINK", "think", "auto"), "thinking 모드: on / off / auto(서버 기본값)")
	effort := flag.String("effort", conf("LLM_REASONING_EFFORT", "reasoning_effort", ""), "reasoning_effort: none / low / medium / high (비우면 안 보냄, Ollama 는 none 으로 thinking 끔)")
	hideThink := flag.Bool("hide-think", false, "추론 과정(reasoning) 출력 숨김")
	chatMode := flag.Bool("chat", false, "대화형 멀티턴 모드")
	scenario := flag.String("scenario", "", "멀티턴 시나리오 JSON 파일 또는 폴더 (쉼표로 여러 개)")
	web := flag.Bool("web", false, "웹 UI 실행 (설정 변경·대화·시나리오)")
	addr := flag.String("addr", defaultAddr, "웹 UI 주소 (-web 일 때)")
	noOpen := flag.Bool("no-open", false, "웹 UI 실행 시 브라우저를 열지 않음")
	noRecord := flag.Bool("no-record", false, "실행 기록(runs/*.jsonl)을 남기지 않음")
	flag.StringVar(&runsFlag, "runs", "", "실행 기록 폴더 (기본: scenarios 폴더 옆의 runs)")
	bench := flag.String("bench", "", "동시 처리 부하 테스트: 동시 수준 목록 (예: 1,2,4,8)")
	benchRequests := flag.Int("bench-requests", 0, "부하 테스트 수준당 요청 수 (0 이면 수준의 2배)")
	flag.Parse()

	if presetID != "" {
		if _, ok := findPreset(presetsFrom(cfg), presetID); !ok {
			fail("프리셋 없음: %q (.env.toml 의 [preset.%s] 를 확인하세요)", presetID, presetID)
		}
	}
	if _, _, err := parseThink(*think); err != nil {
		fail("%v", err)
	}
	if *server, err = normalizeServer(*server); err != nil {
		fail("%v", err)
	}

	opt := Options{
		Client:    &http.Client{Timeout: *timeout},
		Base:      strings.TrimRight(*baseURL, "/"),
		APIKey:    *apiKey,
		Model:     *model,
		Temp:      *temp,
		MaxTokens: *maxTokens,
		Stream:    *stream,
		Effort:    *effort,
		Server:    *server,
	}

	// 웹 UI 는 접속주소가 비어 있어도 띄운다 (화면에서 입력).
	if *web {
		settings := Settings{
			BaseURL: *baseURL, Model: *model, APIKey: *apiKey, Server: *server, Think: *think, Effort: *effort,
			Temperature: *temp, MaxTokens: *maxTokens, System: *system,
		}
		if err := runWeb(*addr, !*noOpen, cfgPath, cfg, settings, *timeout, *scenario); err != nil {
			fail("%v", err)
		}
		return
	}

	if *baseURL == "" {
		fail("접속주소 없음. .env.toml 의 base_url 을 설정하세요 (.env.toml.example 참고)")
	}
	if *model == "" && !*models {
		fail("모델명 없음. .env.toml 의 model 을 설정하세요")
	}

	cliPrice = pricesFrom(cfg)[*model]
	if !*noRecord && !*models {
		cliRuns = newRunStore(runsDir(), nil)
		cliSession = newSession()
	}
	ctx := context.Background()
	if opt.Server == ServerAuto && !*models {
		s, owner, derr := detectServer(ctx, opt)
		opt.Server = s
		if derr != nil {
			fmt.Fprintf(os.Stderr, "[서버 판별 실패, openai 로 진행: %v]\n", derr)
		} else {
			fmt.Fprintf(os.Stderr, "[서버 %s (owned_by=%s)]\n", s, owner)
		}
	}
	switch {
	case *bench != "":
		err = runBenchCLI(ctx, opt, *bench, *benchRequests, *prompt, *system, *think)
	case *models:
		var list []ModelInfo
		if list, err = listModels(ctx, opt); err == nil {
			for _, m := range list {
				fmt.Printf("%s\t(owned_by=%s, max_model_len=%d)\n", m.ID, m.OwnedBy, m.MaxModelLen)
			}
		}
	case *scenario != "":
		var failed int
		if failed, err = runScenarios(ctx, opt, *hideThink, *scenario, *system, *think); err == nil && failed > 0 {
			os.Exit(2)
		}
	case *chatMode:
		err = runChat(ctx, opt, *hideThink, *system, *think)
	default:
		msgs := []Message{{Role: "system", Content: *system}, {Role: "user", Content: *prompt}}
		var r Result
		r, err = completeConsole(ctx, opt, *hideThink, msgs, *think)
		recordTurn(opt, *think, msgs, r, err, "", 0)
		if err == nil {
			printStats(r)
		}
	}
	if err != nil {
		fail("%v", err)
	}
}

// CLI 실행 기록. -no-record 면 nil 이다.
var (
	cliRuns    *runStore
	cliSession string
	cliPrice   Price // 이 모델의 단가 ([price."<모델>"]). 없으면 비용을 안 보인다
)

func recordTurn(opt Options, think string, msgs []Message, r Result, err error, scenario string, turn int) {
	if cliRuns == nil {
		return
	}
	rec := turnRecord("cli", cliSession, opt, think, msgs, r, err)
	rec.Scenario, rec.Turn = scenario, turn
	if werr := cliRuns.append(rec); werr != nil {
		fmt.Fprintln(os.Stderr, "기록 쓰기 실패:", werr)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}

// completeConsole 은 답변을 stdout 에, 추론 과정을 stderr 에 출력하며 호출한다.
func completeConsole(ctx context.Context, opt Options, hideThink bool, msgs []Message, think string) (Result, error) {
	inThink := false
	r, err := complete(ctx, opt, msgs, think, func(kind, text string) {
		switch kind {
		case "reasoning":
			if hideThink {
				return
			}
			if !inThink {
				fmt.Fprint(os.Stderr, "<think>\n")
				inThink = true
			}
			fmt.Fprint(os.Stderr, text)
		case "content":
			if inThink {
				fmt.Fprint(os.Stderr, "\n</think>\n")
				inThink = false
			}
			fmt.Print(text)
		}
	})
	if inThink {
		fmt.Fprint(os.Stderr, "\n</think>\n")
	}
	fmt.Println()
	return r, err
}

func printStats(r Result) {
	var parts []string
	parts = append(parts, "server="+r.Server, fmt.Sprintf("소요 %s", r.Elapsed.Round(time.Millisecond)))
	if r.TTFT > 0 {
		parts = append(parts, fmt.Sprintf("첫토큰 %s", r.TTFT.Round(time.Millisecond)))
	}
	parts = append(parts, fmt.Sprintf("추론 %d자", len([]rune(r.Reasoning))))
	if cliPrice.set() && r.Usage != nil {
		parts = append(parts, "환산 "+fmtUSD(cliPrice.cost(r.Usage)))
	}
	if strings.TrimSpace(r.Content) == "" {
		// 모델이 추론 안에서 답을 끝내고 본문을 비우는 경우가 있다.
		parts = append(parts, "본문 없음")
	}
	if u := r.Usage; u != nil {
		tok := fmt.Sprintf("토큰 prompt=%d completion=%d", u.PromptTokens, u.CompletionTokens)
		if u.CompletionTokensDetails != nil {
			tok += fmt.Sprintf(" reasoning=%d", u.CompletionTokensDetails.ReasoningTokens)
		}
		parts = append(parts, tok)
	}
	if r.Finish != "" && r.Finish != "stop" {
		parts = append(parts, "finish="+r.Finish)
	}
	fmt.Fprintf(os.Stderr, "[%s]\n", strings.Join(parts, " | "))
}

// runChat 은 표준입력으로 대화를 이어 가는 멀티턴 모드다.
func runChat(ctx context.Context, opt Options, hideThink bool, system, think string) error {
	history := []Message{{Role: "system", Content: system}}
	fmt.Fprintln(os.Stderr, "멀티턴 대화 모드. 명령: /think on|off|auto, /effort none|low|medium|high (값 없으면 안 보냄), /reset, /history, /quit")
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1024*1024), 1024*1024)
	for {
		fmt.Fprintf(os.Stderr, "\n[턴 %d | think=%s] > ", len(history)/2+1, think)
		if !in.Scan() {
			return in.Err()
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if cmd, arg, _ := strings.Cut(line, " "); strings.HasPrefix(cmd, "/") {
			switch cmd {
			case "/quit", "/exit", "/q":
				return nil
			case "/reset":
				history = history[:1]
				fmt.Fprintln(os.Stderr, "대화 기록 초기화")
			case "/history":
				for _, m := range history {
					fmt.Fprintf(os.Stderr, "%-9s %s\n", m.Role+":", m.Content)
				}
			case "/think":
				if _, _, err := parseThink(arg); err != nil {
					fmt.Fprintln(os.Stderr, err)
				} else {
					think = arg
				}
			case "/effort":
				opt.Effort = arg
				fmt.Fprintf(os.Stderr, "reasoning_effort=%q\n", opt.Effort)
			default:
				fmt.Fprintln(os.Stderr, "알 수 없는 명령:", cmd)
			}
			continue
		}
		history = append(history, Message{Role: "user", Content: line})
		r, err := completeConsole(ctx, opt, hideThink, history, think)
		recordTurn(opt, think, history, r, err, "", 0)
		if err != nil {
			history = history[:len(history)-1]
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			continue
		}
		printStats(r)
		// 추론 과정은 히스토리에 넣지 않는다 (DeepSeek·Qwen 권장 방식).
		history = append(history, Message{Role: "assistant", Content: r.Content})
	}
}

// runScenarios 는 시나리오 파일을 차례로 돌리고 실패한 턴 수를 돌려준다.
func runScenarios(ctx context.Context, opt Options, hideThink bool, spec, defaultSystem, defaultThink string) (int, error) {
	files, err := scenarioFiles(spec)
	if err != nil {
		return 0, err
	}
	type row struct {
		name                       string
		turns, done, checks, fails int
		elapsed                    time.Duration
		note                       string
		results                    []CheckResult
	}
	var rows []row
	totalFails := 0
	for _, f := range files {
		sc, err := loadScenario(f)
		if err != nil {
			return totalFails, err
		}
		system := defaultSystem
		if sc.System != nil {
			system = *sc.System
		}
		history := []Message{}
		if system != "" {
			history = append(history, Message{Role: "system", Content: system})
		}
		rw := row{name: sc.Name, turns: len(sc.Turns)}
		for i, t := range sc.Turns {
			think := defaultThink
			if sc.Think != "" {
				think = sc.Think
			}
			if t.Think != "" {
				think = t.Think
			}
			fmt.Printf("\n=== [%s] 턴 %d/%d (think=%s) ===\nUSER: %s\nASSISTANT: ", sc.Name, i+1, len(sc.Turns), think, t.User)
			history = append(history, Message{Role: "user", Content: t.User})
			r, err := completeConsole(ctx, opt, hideThink, history, think)
			recordTurn(opt, think, history, r, err, sc.Name, i+1)
			if err != nil {
				left := 0
				for _, x := range sc.Turns[i:] {
					if len(x.Expect) > 0 {
						left++
					}
				}
				if left == 0 {
					left = 1 // 검사가 남지 않았어도 호출 오류는 실패로 센다
				}
				rw.checks += left
				rw.fails += left
				rw.results = append(rw.results, CheckResult{Turn: i + 1, Missing: []string{"(호출 오류)"}})
				rw.note = err.Error()
				fmt.Fprintln(os.Stderr, "ERROR:", err)
				break
			}
			printStats(r)
			rw.elapsed += r.Elapsed
			rw.done++
			history = append(history, Message{Role: "assistant", Content: r.Content})
			if len(t.Expect) > 0 {
				miss := checkExpect(r.Content, t.Expect)
				rw.checks++
				rw.results = append(rw.results, CheckResult{Turn: i + 1, Expect: t.Expect, Missing: miss})
				if len(miss) > 0 {
					rw.fails++
					fmt.Printf("CHECK: FAIL (없음: %s)\n", strings.Join(miss, ", "))
				} else {
					fmt.Printf("CHECK: PASS (%s)\n", strings.Join(t.Expect, ", "))
				}
			}
		}
		totalFails += rw.fails
		rows = append(rows, rw)
		if cliRuns != nil {
			think := defaultThink
			if sc.Think != "" {
				think = "scenario"
			}
			rec := ScenarioRecord{Type: "scenario", Time: time.Now().Format(timeFmt), Source: "cli", Session: cliSession,
				Name: sc.Name, BaseURL: opt.Base, Model: opt.Model, Server: opt.Server, Think: think,
				Turns: rw.turns, Done: rw.done, Checks: rw.checks, Fails: rw.fails, ElapsedMs: rw.elapsed.Milliseconds(),
				Note: rw.note, Results: rw.results}
			if werr := cliRuns.append(rec); werr != nil {
				fmt.Fprintln(os.Stderr, "기록 쓰기 실패:", werr)
			}
		}
	}

	fmt.Println("\n=== 결과 ===")
	for _, r := range rows {
		status := "PASS"
		if r.fails > 0 {
			status = fmt.Sprintf("FAIL %d", r.fails)
		}
		line := fmt.Sprintf("%-24s 턴 %2d  %-7s %s", r.name, r.turns, status, r.elapsed.Round(time.Millisecond))
		if r.note != "" {
			line += "  " + r.note
		}
		fmt.Println(line)
	}
	return totalFails, nil
}

// runBenchCLI 는 부하 테스트를 돌리고 수준별 표를 출력한다.
func runBenchCLI(ctx context.Context, opt Options, spec string, requests int, prompt, system, think string) error {
	levels, err := parseLevels(spec)
	if err != nil {
		return err
	}
	cfg := BenchConfig{Levels: levels, Requests: requests, Prompt: prompt, System: system, Think: think}
	fmt.Fprintf(os.Stderr, "부하 테스트: 동시 %v, 수준당 요청 %s, max_tokens %d, think=%s\n", levels, map[bool]string{true: "수준의 2배", false: strconv.Itoa(requests)}[requests <= 0], opt.MaxTokens, think)
	fmt.Printf("%6s %6s %5s %7s %9s %10s %9s %9s %9s %9s\n", "동시", "요청", "오류", "req/s", "tok/s", "요청당tok/s", "p50", "p95", "TTFT p50", "TTFT p95")
	res, err := runBench(ctx, opt, cfg, nil, func(b BenchLevel) {
		ms := func(v int64) string { return (time.Duration(v) * time.Millisecond).Round(time.Millisecond).String() }
		fmt.Printf("%6d %6d %5d %7.2f %9.1f %10.1f %9s %9s %9s %9s\n", b.Level, b.Requests, b.Errors, b.RPS, b.TokPerSec, b.ReqTokPerS, ms(b.LatP50ms), ms(b.LatP95ms), ms(b.TTFTP50ms), ms(b.TTFTP95ms))
		if b.FirstError != "" {
			fmt.Printf("       첫 오류: %s\n", b.FirstError)
		}
	})
	rec := recommendLevel(res)
	if rec > 0 {
		fmt.Printf("\n권장 동시 수: %d (오류 없이 처리량이 10%% 이상 늘어난 마지막 수준)\n", rec)
	} else {
		fmt.Println("\n권장 동시 수: 판단할 수 없음 (첫 수준부터 오류)")
	}
	if cliRuns != nil {
		r := BenchRecord{Type: "bench", Time: time.Now().Format(timeFmt), Source: "cli", BaseURL: opt.Base, Model: opt.Model, Server: opt.Server,
			Think: think, MaxTokens: opt.MaxTokens, Prompt: prompt, Requests: requests, Levels: res, Recommend: rec, Stopped: err != nil}
		if werr := cliRuns.append(r); werr != nil {
			fmt.Fprintln(os.Stderr, "기록 쓰기 실패:", werr)
		}
	}
	return err
}
