package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// ── 累积器单测 ────────────────────────────────────────────────────────────────

func TestClaudeStreamAccumulatorAssemblesToolUse(t *testing.T) {
	t.Parallel()
	accum := newClaudeStreamAccumulator()
	feed := func(raw string) {
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
		accum.handle(data)
	}
	feed(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`)
	feed(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"need search"}}`)
	feed(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig123"}}`)
	feed(`{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`)
	feed(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"我先搜索一下。"}}`)
	feed(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_01","name":"web_search"}}`)
	// input_json 跨帧分片
	feed(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"20"}}`)
	feed(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"26 news\"}"}}`)
	feed(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`)

	if accum.stopReason != "tool_use" {
		t.Fatalf("stopReason = %q", accum.stopReason)
	}
	calls := accum.toolCalls()
	if len(calls) != 1 || calls[0].ID != "toolu_01" || calls[0].Name != "web_search" {
		t.Fatalf("calls = %+v", calls)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Args, &args); err != nil || args["query"] != "2026 news" {
		t.Fatalf("args = %s", calls[0].Args)
	}
	blocks := accum.assistantBlocks()
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[0]["type"] != "thinking" || blocks[0]["signature"] != "sig123" {
		t.Fatalf("thinking block must carry signature: %+v", blocks[0])
	}
	if blocks[2]["type"] != "tool_use" {
		t.Fatalf("blocks[2] = %+v", blocks[2])
	}
}

func TestOpenAIStreamFilterAccumulatesAndSuppresses(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	filter := newOpenAIStreamFilter(&out)
	frames := []string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"content":"查一下。"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"que"}}]}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ry\":\"x\"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
		`data: {"choices":[],"usage":{"prompt_tokens":5}}` + "\n\n",
		"data: [DONE]\n\n",
	}
	for _, frame := range frames {
		if _, err := filter.Write([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	got := out.String()
	if !strings.Contains(got, "查一下。") {
		t.Fatalf("content should pass through: %s", got)
	}
	if strings.Contains(got, "tool_calls") || strings.Contains(got, "[DONE]") || strings.Contains(got, "prompt_tokens") {
		t.Fatalf("tool_calls/usage/[DONE] must be absorbed: %s", got)
	}
	if filter.finishReason != "tool_calls" {
		t.Fatalf("finishReason = %q", filter.finishReason)
	}
	calls := filter.accumulated()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "web_search" {
		t.Fatalf("calls = %+v", calls)
	}
	if string(calls[0].Args) != `{"query":"x"}` {
		t.Fatalf("args = %s", calls[0].Args)
	}
	if filter.content.String() != "查一下。" {
		t.Fatalf("content accum = %q", filter.content.String())
	}
}

// ── 伪 Host:脚本化 gateway.forward 流帧,驱动 loop 状态机 ────────────────────

type fakeHostStream struct {
	frames []sdk.HostStreamFrame
	pos    int
}

func (s *fakeHostStream) Send(sdk.HostStreamFrame) error { return nil }
func (s *fakeHostStream) CloseSend() error               { return nil }
func (s *fakeHostStream) Recv() (*sdk.HostStreamFrame, error) {
	if s.pos >= len(s.frames) {
		return nil, io.EOF
	}
	frame := s.frames[s.pos]
	s.pos++
	return &frame, nil
}

type fakeHost struct {
	bodies   [][]byte
	payloads []map[string]any
	respond  func(call int, body []byte) []sdk.HostStreamFrame
}

func (h *fakeHost) Invoke(context.Context, sdk.HostInvokeRequest) (*sdk.HostInvokeResponse, error) {
	return &sdk.HostInvokeResponse{Status: "ok", Payload: map[string]any{}}, nil
}

func (h *fakeHost) InvokeStream(_ context.Context, req sdk.HostStreamRequest) (sdk.HostStream, error) {
	body, _ := req.Payload["body"].(string)
	h.bodies = append(h.bodies, []byte(body))
	h.payloads = append(h.payloads, req.Payload)
	return &fakeHostStream{frames: h.respond(len(h.bodies), []byte(body))}, nil
}

func sseDataFrames(payloads ...string) []sdk.HostStreamFrame {
	frames := make([]sdk.HostStreamFrame, 0, len(payloads)+1)
	for _, p := range payloads {
		frames = append(frames, sdk.HostStreamFrame{
			Event:   "chunk",
			Payload: map[string]any{"status_code": 200, "data": "data: " + p + "\n\n"},
		})
	}
	frames = append(frames, sdk.HostStreamFrame{
		Done: true,
		Payload: map[string]any{
			"usage": map[string]any{
				"model":     "claude-sonnet-5",
				"user_cost": 0.01,
				"metrics":   []any{map[string]any{"key": "output_tokens", "value": float64(10)}},
			},
		},
	})
	return frames
}

type staticSearchProvider struct{ calls int }

func (s *staticSearchProvider) Name() string { return "static" }
func (s *staticSearchProvider) Search(context.Context, string, searchOptions) (*searchResponse, error) {
	s.calls++
	return &searchResponse{Results: []searchResult{{Title: "T1", URL: "https://example.com", Snippet: "snip", Content: "full content"}}}, nil
}

type terminalTestTool struct{}

func (terminalTestTool) Name() string        { return "generate_test_file" }
func (terminalTestTool) Description() string { return "Generate a test file and finish." }
func (terminalTestTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false}
}
func (terminalTestTool) Execute(context.Context, *toolContext, json.RawMessage) (*toolOutcome, error) {
	return &toolOutcome{
		ForModel:        "file generated",
		ForClient:       map[string]any{"file": map[string]any{"name": "test.pdf"}},
		Terminal:        true,
		TerminalMessage: "文件已生成，可通过文件卡下载。",
	}, nil
}

func newLoopTestPlugin(host sdk.Host, provider searchProvider) *Plugin {
	tuning := defaultChatTuning()
	toolCfg := defaultToolSettings()
	toolCfg.WebSearchEnabled = true
	toolCfg.TavilyAPIKey = "test-key"
	return &Plugin{
		logger:         slog.Default(),
		host:           host,
		tuning:         &tuning,
		toolCfg:        &toolCfg,
		searchProvider: provider,
	}
}

func TestClaudeToolLoopSearchThenAnswer(t *testing.T) {
	t.Parallel()
	host := &fakeHost{}
	host.respond = func(call int, body []byte) []sdk.HostStreamFrame {
		switch call {
		case 1:
			// 第一轮:模型请求 web_search
			return sseDataFrames(
				`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"我先搜索。"}}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"web_search"}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"hopbase\"}"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			)
		default:
			// 第二轮:请求体必须带回放的 assistant 回合与 tool_result
			var req map[string]any
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("second call body invalid: %v", err)
			}
			msgs, _ := req["messages"].([]any)
			encoded, _ := json.Marshal(msgs)
			if !strings.Contains(string(encoded), "tool_result") || !strings.Contains(string(encoded), "toolu_01") {
				t.Errorf("second call missing tool_result turn: %s", encoded)
			}
			return sseDataFrames(
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"根据搜索[1],答案是…"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			)
		}
	}
	provider := &staticSearchProvider{}
	p := newLoopTestPlugin(host, provider)

	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"搜一下 hopbase"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(headerUserID, "7")
	rec := httptest.NewRecorder()

	var parsed openAIChatRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	opts := defaultCompileOpts()
	p.runToolLoop(req.Context(), rec, req, "claude", parsed, body, opts, p.enabledChatTools(), slog.Default())

	out := rec.Body.String()
	if provider.calls != 1 {
		t.Fatalf("search calls = %d, want 1", provider.calls)
	}
	if len(host.bodies) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(host.bodies))
	}
	for _, want := range []string{
		`"tool_call_started"`, `"tool_call_finished"`, `"sources"`,
		"我先搜索。", "根据搜索[1],答案是…",
		`"iterations":2`, `"tool_calls":1`,
		`"finish_reason":"stop"`, "data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// 首轮请求必须带工具声明
	if !strings.Contains(string(host.bodies[0]), `"web_search"`) || !strings.Contains(string(host.bodies[0]), "input_schema") {
		t.Fatalf("first call missing tool declarations: %s", host.bodies[0])
	}
	// usage 聚合:两轮 0.01 → 0.02
	if !strings.Contains(out, `"user_cost":0.02`) {
		t.Fatalf("aggregated usage missing: %s", out)
	}
}

func TestClaudeToolLoopReportsOutputLimit(t *testing.T) {
	t.Parallel()
	host := &fakeHost{}
	host.respond = func(_ int, _ []byte) []sdk.HostStreamFrame {
		return sseDataFrames(
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"未完成内容"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`,
		)
	}
	p := newLoopTestPlugin(host, nil)
	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"写长文"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(headerUserID, "7")
	rec := httptest.NewRecorder()
	var parsed openAIChatRequest
	_ = json.Unmarshal(body, &parsed)

	p.runToolLoop(req.Context(), rec, req, "claude", parsed, body, defaultCompileOpts(), p.enabledChatTools(), slog.Default())

	out := rec.Body.String()
	for _, want := range []string{`"finish_reason":"length"`, `"stop_reason":"max_tokens"`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestClaudeTerminalToolSkipsSecondModelCall(t *testing.T) {
	t.Parallel()
	host := &fakeHost{}
	host.respond = func(call int, _ []byte) []sdk.HostStreamFrame {
		if call != 1 {
			t.Fatalf("unexpected upstream call %d", call)
		}
		return sseDataFrames(
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_file","name":"generate_test_file"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		)
	}
	p := newLoopTestPlugin(host, nil)
	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"生成文件"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(headerUserID, "7")
	rec := httptest.NewRecorder()
	var parsed openAIChatRequest
	_ = json.Unmarshal(body, &parsed)

	p.runToolLoop(req.Context(), rec, req, "claude", parsed, body, defaultCompileOpts(), []chatTool{terminalTestTool{}}, slog.Default())

	if len(host.bodies) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(host.bodies))
	}
	if host.payloads[0]["trace_id"] == "" || !strings.HasSuffix(host.payloads[0]["request_id"].(string), ":1") {
		t.Fatalf("terminal usage trace missing: %+v", host.payloads[0])
	}
	out := rec.Body.String()
	for _, want := range []string{`"tool_call_finished"`, `"name":"test.pdf"`, "文件已生成", `"iterations":1`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"user_cost":0.02`) || !strings.Contains(out, `"user_cost":0.01`) {
		t.Fatalf("terminal tool must aggregate one model usage only: %s", out)
	}
}

func TestClaudeToolLoopForcedFinalAtMaxIterations(t *testing.T) {
	t.Parallel()
	host := &fakeHost{}
	host.respond = func(call int, body []byte) []sdk.HostStreamFrame {
		if strings.Contains(string(body), `"tool_choice":{"type":"none"}`) {
			return sseDataFrames(
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"最终答案"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			)
		}
		// 每轮都请求工具,逼 loop 触达上限
		return sseDataFrames(
			fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_%d","name":"web_search"}}`, call),
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"q\"}"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		)
	}
	provider := &staticSearchProvider{}
	p := newLoopTestPlugin(host, provider)
	p.toolCfg.MaxIterations = 2
	p.toolCfg.MaxSearchesPerMessage = 10

	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"loop"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(headerUserID, "7")
	rec := httptest.NewRecorder()
	var parsed openAIChatRequest
	_ = json.Unmarshal(body, &parsed)
	p.runToolLoop(req.Context(), rec, req, "claude", parsed, body, defaultCompileOpts(), p.enabledChatTools(), slog.Default())

	// MaxIterations=2 → 第 3 轮必须是 tool_choice none 的强制收尾
	if len(host.bodies) != 3 {
		t.Fatalf("upstream calls = %d, want 3 (2 tool rounds + forced final)", len(host.bodies))
	}
	if !strings.Contains(string(host.bodies[2]), `"tool_choice":{"type":"none"}`) {
		t.Fatalf("final call must disable tools: %s", host.bodies[2])
	}
	if !strings.Contains(rec.Body.String(), "最终答案") {
		t.Fatalf("final answer missing: %s", rec.Body.String())
	}
}

func TestOpenAIToolLoopSearchThenAnswer(t *testing.T) {
	t.Parallel()
	host := &fakeHost{}
	host.respond = func(call int, body []byte) []sdk.HostStreamFrame {
		switch call {
		case 1:
			return sseDataFrames(
				`{"choices":[{"index":0,"delta":{"content":"让我搜搜。"}}]}`,
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"y\"}"}}]}}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			)
		default:
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			encoded, _ := json.Marshal(req["messages"])
			if !strings.Contains(string(encoded), `"tool_call_id":"call_9"`) || !strings.Contains(string(encoded), `"role":"tool"`) {
				t.Errorf("second call missing tool turn: %s", encoded)
			}
			return sseDataFrames(
				`{"choices":[{"index":0,"delta":{"content":"答案来了"}}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			)
		}
	}
	provider := &staticSearchProvider{}
	p := newLoopTestPlugin(host, provider)

	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"搜 y"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(headerUserID, "7")
	rec := httptest.NewRecorder()
	var parsed openAIChatRequest
	_ = json.Unmarshal(body, &parsed)
	p.runToolLoop(req.Context(), rec, req, "openai", parsed, body, defaultCompileOpts(), p.enabledChatTools(), slog.Default())

	out := rec.Body.String()
	if len(host.bodies) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(host.bodies))
	}
	if !strings.Contains(string(host.bodies[0]), `"type":"function"`) {
		t.Fatalf("first call missing openai tool declarations: %s", host.bodies[0])
	}
	for _, want := range []string{"让我搜搜。", "答案来了", `"tool_call_finished"`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestOpenAITerminalToolSkipsSecondModelCall(t *testing.T) {
	t.Parallel()
	host := &fakeHost{}
	host.respond = func(call int, _ []byte) []sdk.HostStreamFrame {
		if call != 1 {
			t.Fatalf("unexpected upstream call %d", call)
		}
		return sseDataFrames(
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_file","type":"function","function":{"name":"generate_test_file","arguments":"{}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		)
	}
	p := newLoopTestPlugin(host, nil)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"生成文件"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(headerUserID, "7")
	rec := httptest.NewRecorder()
	var parsed openAIChatRequest
	_ = json.Unmarshal(body, &parsed)

	p.runToolLoop(req.Context(), rec, req, "openai", parsed, body, defaultCompileOpts(), []chatTool{terminalTestTool{}}, slog.Default())

	if len(host.bodies) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(host.bodies))
	}
	out := rec.Body.String()
	for _, want := range []string{`"tool_call_finished"`, `"name":"test.pdf"`, "文件已生成", `"iterations":1`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"user_cost":0.02`) || !strings.Contains(out, `"user_cost":0.01`) {
		t.Fatalf("terminal tool must aggregate one model usage only: %s", out)
	}
}

func TestMergeUsageAggregatesTokenMetricsAndCostDetails(t *testing.T) {
	t.Parallel()
	usage := mergeUsage(nil, &sdk.Usage{
		Model: "m", AccountCost: 0.3, UserCost: 0.4, BillingMultiplier: 2,
		Metrics:     []sdk.UsageMetric{{Key: "input_tokens", Kind: "token", Value: 10, AccountCost: 0.1}},
		CostDetails: []sdk.UsageCostDetail{{Key: "input_tokens", AccountCost: 0.1, UserCost: 0.2}},
	})
	usage = mergeUsage(usage, &sdk.Usage{
		Model: "m", AccountCost: 0.5, UserCost: 0.6, BillingMultiplier: 2,
		Metrics: []sdk.UsageMetric{
			{Key: "input_tokens", Kind: "token", Value: 5, AccountCost: 0.05},
			{Key: "output_tokens", Kind: "token", Value: 3, AccountCost: 0.45},
			{Key: "cache_creation_input_tokens", Kind: "token", Value: 7, AccountCost: 0.02},
			{Key: "cache_read_input_tokens", Kind: "token", Value: 11, AccountCost: 0.01},
		},
		CostDetails: []sdk.UsageCostDetail{
			{Key: "output_tokens", AccountCost: 0.45, UserCost: 0.6},
			{Key: "cache_creation", AccountCost: 0.02, UserCost: 0.04},
			{Key: "cache_read", AccountCost: 0.01, UserCost: 0.02},
		},
	})
	if usage.AccountCost != 0.8 || usage.UserCost != 1 {
		t.Fatalf("aggregated costs = %v/%v, want 0.8/1", usage.AccountCost, usage.UserCost)
	}
	if len(usage.Metrics) != 4 || usage.Metrics[0].Value != 15 || usage.Metrics[1].Value != 3 || usage.Metrics[2].Value != 7 || usage.Metrics[3].Value != 11 {
		t.Fatalf("aggregated metrics = %+v", usage.Metrics)
	}
	if len(usage.CostDetails) != 4 {
		t.Fatalf("cost details count = %d, want 4", len(usage.CostDetails))
	}
}

func TestMergeUsageKeepsModelTokensSeparateFromRenderUsage(t *testing.T) {
	t.Parallel()
	usage := mergeUsage(nil, &sdk.Usage{
		Model: "claude-sonnet-5", UserCost: 0.12,
		Metrics: []sdk.UsageMetric{
			{Key: "input_tokens", Kind: "token", Value: 1200},
			{Key: "output_tokens", Kind: "token", Value: 400},
		},
	})
	usage = mergeUsage(usage, &sdk.Usage{
		Model: "document-render-pdf", UserCost: 0.03,
		Metrics:     []sdk.UsageMetric{{Key: "document_render", Kind: "custom", Value: 1}},
		CostDetails: []sdk.UsageCostDetail{{Key: "document_render", UserCost: 0.03}},
	})
	if usage.Model != "claude-sonnet-5" {
		t.Fatalf("render usage replaced model identity: %q", usage.Model)
	}
	if usage.UserCost != 0.15 {
		t.Fatalf("total cost = %v, want 0.15", usage.UserCost)
	}
	values := map[string]float64{}
	for _, metric := range usage.Metrics {
		values[metric.Key] = metric.Value
	}
	if values["input_tokens"] != 1200 || values["output_tokens"] != 400 || values["document_render"] != 1 {
		t.Fatalf("metrics mixed or lost: %+v", values)
	}
}

func TestFileToolDeclarationPayloadStaysBounded(t *testing.T) {
	t.Parallel()
	tools := []chatTool{
		&generateDocumentTool{formats: []string{"pdf", "docx"}},
		&generateSpreadsheetTool{},
		&generatePresentationTool{},
	}
	claudePayload, err := json.Marshal(claudeToolDeclarations(tools))
	if err != nil {
		t.Fatal(err)
	}
	openAIPayload, err := json.Marshal(openAIToolDeclarations(tools))
	if err != nil {
		t.Fatal(err)
	}
	const maxDeclarationBytes = 16 << 10
	if len(claudePayload) > maxDeclarationBytes || len(openAIPayload) > maxDeclarationBytes {
		t.Fatalf("file tool declarations grew unexpectedly: claude=%d openai=%d limit=%d", len(claudePayload), len(openAIPayload), maxDeclarationBytes)
	}
	t.Logf("file tool declaration bytes: claude=%d openai=%d", len(claudePayload), len(openAIPayload))
}

// ── Tavily / web_search 工具 ─────────────────────────────────────────────────

func TestTavilyClientParsesAndRetries(t *testing.T) {
	t.Parallel()
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth header = %q", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"Doc","url":"https://d.example","content":"body text"}]}`))
	}))
	defer server.Close()

	client := newTavilyClient("k")
	client.endpoint = server.URL
	resp, err := client.Search(context.Background(), "q", searchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want retry once on 5xx", attempts)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://d.example" {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestWebSearchToolLimitAndErrors(t *testing.T) {
	t.Parallel()
	provider := &staticSearchProvider{}
	tool := &webSearchTool{provider: provider, maxPerMessage: 1}
	tc := &toolContext{logger: slog.Default()}

	out, err := tool.Execute(context.Background(), tc, json.RawMessage(`{"query":"a"}`))
	if err != nil || out.IsError {
		t.Fatalf("first search should succeed: %+v err=%v", out, err)
	}
	if sources, ok := out.ForClient["sources"].([]any); !ok || len(sources) != 1 {
		t.Fatalf("ForClient = %+v", out.ForClient)
	}
	out, _ = tool.Execute(context.Background(), tc, json.RawMessage(`{"query":"b"}`))
	if !out.IsError || !strings.Contains(out.ForModel, "Search limit for this message reached") {
		t.Fatalf("second search should hit limit: %+v", out)
	}
	out, _ = tool.Execute(context.Background(), tc, json.RawMessage(`{}`))
	if !out.IsError {
		t.Fatalf("empty query should be tool error: %+v", out)
	}
}

// 2026-09-11 review 回归：SSE error 帧的 code 必须跟着 message 走。
// 帧里写死 upstream_error 会让前端按通用兜底句本地化，把「管理员没授权该模型」
// 这条可执行提示抹掉——每种语言都抹，英文也抹（playground#7/#8 的修复会被顶掉）。
func TestWriteSSEErrorFrameCarriesMatchingCode(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writeSSEErrorFrame(recorder, chatErrMemberGroupForbidden, chatCodeMemberGroupForbidden)
	body := recorder.Body.String()
	if !strings.Contains(body, chatErrMemberGroupForbidden) {
		t.Fatalf("body = %q, want the specific member-group message", body)
	}
	if !strings.Contains(body, `"code":"`+chatCodeMemberGroupForbidden+`"`) {
		t.Fatalf("body = %q, want code %q so the UI does not swap in the generic fallback", body, chatCodeMemberGroupForbidden)
	}
	if strings.Contains(body, chatCodeUpstreamUnavailable) {
		t.Fatalf("body = %q, must not ship the specific message under the generic code", body)
	}

	recorder = httptest.NewRecorder()
	writeSSEErrorFrame(recorder, chatErrUpstreamUnavailable, chatCodeUpstreamUnavailable)
	if body := recorder.Body.String(); !strings.Contains(body, `"code":"`+chatCodeUpstreamUnavailable+`"`) {
		t.Fatalf("body = %q, want %q", body, chatCodeUpstreamUnavailable)
	}
}
