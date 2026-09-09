package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// ── 服务端工具循环 ────────────────────────────────────────────────────────────
// 模型请求工具 → 服务端执行 → 结果喂回 → 继续生成,直到模型给出终答或触达
// 上限。文本/思考增量实时流给前端,工具生命周期经 airgate.tool_event 下发。
// 每次上游调用都经 gateway.forward 由 core 独立计费,末尾 usage 帧只是
// 聚合展示口径。仅流式请求走本路径(前端恒 stream)。

const (
	toolLoopMaxWallClock = 5 * time.Minute
	// 强制收尾:达迭代上限后最后一轮禁用工具,逼模型出文本终答。
	claudeStopReasonToolUse = "tool_use"
)

type toolLoopStats struct {
	iterations         int
	toolCalls          int
	usage              *sdk.Usage
	finishReason       string
	upstreamStopReason string
}

func (p *Plugin) runToolLoop(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	platform string,
	req openAIChatRequest,
	body []byte,
	opts compileOpts,
	tools []chatTool,
	logger *slog.Logger,
) {
	userID := int64(parseUserID(r))
	// 工具产物归属:带 conversation_id 时先做归属校验(未提交前可正常报错)。
	if req.ConversationID > 0 && p.svc != nil {
		if _, err := p.svc.GetConversation(ctx, parseUserID(r), req.ConversationID); err != nil {
			writeOpenAIError(w, http.StatusForbidden, "invalid_request_error", "invalid_request", "conversation not found")
			return
		}
	}

	tc := &toolContext{
		userID:         userID,
		conversationID: req.ConversationID,
		requestID:      sdk.ExtractOrGenerateRequestID(r.Header),
		logger:         logger,
	}

	// 提交 SSE 响应头。此后所有错误都以 SSE error 帧下发。
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flushIfPossible(w)

	stats := &toolLoopStats{}
	start := time.Now()
	var loopErr error
	switch platform {
	case "claude", "anthropic":
		loopErr = p.runClaudeToolLoop(ctx, w, r, req, opts, tools, tc, stats, start, logger)
	case "openai":
		loopErr = p.runOpenAIToolLoop(ctx, w, r, req, body, opts, tools, tc, stats, start, logger)
	default:
		loopErr = fmt.Errorf("unsupported platform: %s", platform)
	}
	if loopErr != nil {
		if ctx.Err() != nil {
			logger.Info("tool_loop_cancelled", "iterations", stats.iterations, "tool_calls", stats.toolCalls)
			return
		}
		logger.Warn("tool_loop_failed", sdk.LogFieldError, loopErr, "iterations", stats.iterations)
		// 响应头已提交，只能以 SSE error 帧下发；成员分组白名单拒绝同样要给明确提示
		if p.forwardErrorIsMemberGroupForbidden(ctx, loopErr, int64(parseUserID(r)), platform) {
			writeSSEErrorFrame(w, chatErrMemberGroupForbidden)
			return
		}
		writeSSEErrorFrame(w, "请求暂时无法完成，请稍后重试")
		return
	}

	writeAggregatedUsage(w, stats.usage, stats.iterations, stats.toolCalls)
	writeChatFinish(w, req.Model, stats.finishReason, stats.upstreamStopReason)
	logger.Debug("tool_loop_completed",
		"iterations", stats.iterations,
		"tool_calls", stats.toolCalls,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func writeSSEErrorFrame(w http.ResponseWriter, message string) {
	_ = sseWriter{w: w}.writeJSON(map[string]any{
		"error": map[string]any{"message": message, "type": "server_error", "code": "upstream_error"},
	})
	flushIfPossible(w)
}

// ── Claude 循环 ───────────────────────────────────────────────────────────────

func (p *Plugin) runClaudeToolLoop(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	req openAIChatRequest,
	opts compileOpts,
	tools []chatTool,
	tc *toolContext,
	stats *toolLoopStats,
	start time.Time,
	logger *slog.Logger,
) error {
	working, err := buildClaudeMessagesRequest(req, opts)
	if err != nil {
		return err
	}
	working.Stream = true
	working.Tools = claudeToolDeclarations(tools)
	cacheOn := opts.Tuning.PromptCache && !opts.DisableCache
	maxIter := p.toolSettingsValue().MaxIterations
	forcedFinal := false

	for {
		stats.iterations++
		if forcedFinal {
			working.ToolChoice = map[string]any{"type": "none"}
		}
		payload, err := json.Marshal(working)
		if err != nil {
			return err
		}
		if err := validateChatForwardBodySize(len(payload)); err != nil {
			return err
		}

		accum := newClaudeStreamAccumulator()
		bridge := &claudeSSEBridge{w: w, model: req.Model, suppressFinish: true, onEvent: accum.handle}
		usage, failStatus, failBody, err := p.streamLoopIteration(ctx, r, "claude", "/v1/messages", payload, tc.requestID, stats.iterations, func(data []byte) error {
			_, err := bridge.Write(data)
			if err == nil {
				flushIfPossible(w)
			}
			return err
		})
		if err != nil {
			return err
		}
		if failStatus > 0 {
			return fmt.Errorf("upstream %d: %s", failStatus, truncateForModel(string(failBody), 300))
		}
		if err := bridge.Flush(); err != nil {
			return err
		}
		stats.usage = mergeUsage(stats.usage, usage)
		stats.finishReason = accum.stopReason
		stats.upstreamStopReason = accum.stopReason

		calls := accum.toolCalls()
		if len(calls) == 0 || accum.stopReason != claudeStopReasonToolUse || forcedFinal {
			return nil
		}

		// 回放 assistant 回合(含 thinking signature)+ 执行工具 + 喂回结果
		working.Messages = append(working.Messages, claudeMessage{Role: "assistant", Content: accum.assistantBlocks()})
		results := p.executeToolCalls(ctx, w, tools, calls, tc, stats, logger)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if terminal := terminalToolResult(results); terminal != nil {
			stats.finishReason = "stop"
			if terminal.TerminalMessage != "" {
				if err := writeOpenAIContentDelta(w, req.Model, terminal.TerminalMessage); err != nil {
					return err
				}
				flushIfPossible(w)
			}
			return nil
		}
		blocks := make([]claudeBlock, 0, len(results))
		for _, res := range results {
			blocks = append(blocks, claudeBlock{
				"type":        "tool_result",
				"tool_use_id": res.call.ID,
				"content":     res.outcome.ForModel,
				"is_error":    res.outcome.IsError,
			})
		}
		working.Messages = append(working.Messages, claudeMessage{Role: "user", Content: blocks})
		if cacheOn {
			setClaudeMessageCacheBreakpoint(working)
		}

		if stats.iterations >= maxIter || time.Since(start) > toolLoopMaxWallClock {
			forcedFinal = true
		}
	}
}

// ── OpenAI 循环 ──────────────────────────────────────────────────────────────

func (p *Plugin) runOpenAIToolLoop(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	req openAIChatRequest,
	body []byte,
	opts compileOpts,
	tools []chatTool,
	tc *toolContext,
	stats *toolLoopStats,
	start time.Time,
	logger *slog.Logger,
) error {
	working, err := buildOpenAIChatBodyMap(req, body, opts)
	if err != nil {
		return err
	}
	if encoded, err := json.Marshal(true); err == nil {
		working["stream"] = encoded
	}
	if encoded, err := json.Marshal(openAIToolDeclarations(tools)); err == nil {
		working["tools"] = encoded
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(working["messages"], &messages); err != nil {
		return err
	}
	maxIter := p.toolSettingsValue().MaxIterations
	forcedFinal := false

	for {
		stats.iterations++
		if forcedFinal {
			if encoded, err := json.Marshal("none"); err == nil {
				working["tool_choice"] = encoded
			}
		}
		if encoded, err := json.Marshal(messages); err == nil {
			working["messages"] = encoded
		}
		payload, err := json.Marshal(working)
		if err != nil {
			return err
		}
		if err := validateChatForwardBodySize(len(payload)); err != nil {
			return err
		}

		filter := newOpenAIStreamFilter(w)
		usage, failStatus, failBody, err := p.streamLoopIteration(ctx, r, "openai", "/v1/chat/completions", payload, tc.requestID, stats.iterations, func(data []byte) error {
			_, err := filter.Write(data)
			return err
		})
		if err != nil {
			return err
		}
		if failStatus > 0 {
			return fmt.Errorf("upstream %d: %s", failStatus, truncateForModel(string(failBody), 300))
		}
		if err := filter.Flush(); err != nil {
			return err
		}
		stats.usage = mergeUsage(stats.usage, usage)
		stats.finishReason = filter.finishReason
		stats.upstreamStopReason = filter.finishReason

		calls := filter.accumulated()
		if len(calls) == 0 || forcedFinal {
			return nil
		}

		// assistant 回合(文本 + tool_calls)+ 工具结果回合
		assistantCalls := make([]map[string]any, 0, len(calls))
		for _, call := range calls {
			assistantCalls = append(assistantCalls, map[string]any{
				"id":   call.ID,
				"type": "function",
				"function": map[string]any{
					"name":      call.Name,
					"arguments": string(call.Args),
				},
			})
		}
		assistantMsg := map[string]any{"role": "assistant", "tool_calls": assistantCalls}
		if content := filter.content.String(); content != "" {
			assistantMsg["content"] = content
		} else {
			assistantMsg["content"] = nil
		}
		if encoded, err := json.Marshal(assistantMsg); err == nil {
			messages = append(messages, encoded)
		}

		results := p.executeToolCalls(ctx, w, tools, calls, tc, stats, logger)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if terminal := terminalToolResult(results); terminal != nil {
			stats.finishReason = "stop"
			if terminal.TerminalMessage != "" {
				if err := writeOpenAIContentDelta(w, req.Model, terminal.TerminalMessage); err != nil {
					return err
				}
				flushIfPossible(w)
			}
			return nil
		}
		for _, res := range results {
			toolMsg := map[string]any{
				"role":         "tool",
				"tool_call_id": res.call.ID,
				"content":      res.outcome.ForModel,
			}
			if encoded, err := json.Marshal(toolMsg); err == nil {
				messages = append(messages, encoded)
			}
		}

		if stats.iterations >= maxIter || time.Since(start) > toolLoopMaxWallClock {
			forcedFinal = true
		}
	}
}

// streamLoopIteration 执行一次上游流式调用。上游非 2xx 时缓冲错误体返回,
// 2xx 数据交给 sink(bridge / filter)。响应头已提交,不再透传上游头。
func (p *Plugin) streamLoopIteration(
	ctx context.Context,
	r *http.Request,
	platform, path string,
	payload []byte,
	traceID string,
	iteration int,
	sink func([]byte) error,
) (usage *sdk.Usage, failStatus int, failBody []byte, err error) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Airgate-Platform", platform)
	headers.Set("Accept", "text/event-stream")

	var model string
	var parsed struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(payload, &parsed) == nil {
		model = parsed.Model
	}
	const maxFailBody = 64 << 10
	err = hostForwardStream(ctx, p.host, hostForwardRequest{
		UserID:    int64(parseUserID(r)),
		GroupID:   0,
		RequestID: fmt.Sprintf("%s:%d", traceID, iteration),
		TraceID:   traceID,
		Model:     model,
		Method:    http.MethodPost,
		Path:      path,
		Headers:   headers,
		Body:      payload,
		Stream:    true,
	}, func(chunk hostForwardChunk) error {
		if chunk.Done {
			usage = chunk.Usage
			return nil
		}
		if failStatus == 0 && chunk.StatusCode >= http.StatusBadRequest {
			failStatus = chunk.StatusCode
		}
		if failStatus > 0 {
			if len(chunk.Data) > 0 && len(failBody) < maxFailBody {
				failBody = append(failBody, chunk.Data...)
			}
			return nil
		}
		if len(chunk.Data) > 0 {
			return sink(chunk.Data)
		}
		return nil
	})
	return usage, failStatus, failBody, err
}

// ── 工具执行 ─────────────────────────────────────────────────────────────────

type toolExecResult struct {
	call    accumToolCall
	outcome *toolOutcome
}

func (p *Plugin) executeToolCalls(
	ctx context.Context,
	w http.ResponseWriter,
	tools []chatTool,
	calls []accumToolCall,
	tc *toolContext,
	stats *toolLoopStats,
	logger *slog.Logger,
) []toolExecResult {
	byName := make(map[string]chatTool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	results := make([]toolExecResult, 0, len(calls))
	for _, call := range calls {
		if ctx.Err() != nil {
			p.auditToolCall(tc, stats.iterations, call, "cancelled", "", 0, 0)
			results = append(results, toolExecResult{call: call, outcome: &toolOutcome{ForModel: "请求已取消", IsError: true}})
			continue
		}
		stats.toolCalls++
		var eventArgs any
		_ = json.Unmarshal(call.Args, &eventArgs)
		_ = writeToolEvent(w, "tool_call_started", stats.iterations, toolEventCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: eventArgs,
		})

		tool := byName[call.Name]
		began := time.Now()
		var outcome *toolOutcome
		if tool == nil {
			outcome = &toolOutcome{ForModel: "未知工具: " + call.Name, IsError: true}
		} else {
			var execErr error
			outcome, execErr = tool.Execute(ctx, tc, call.Args)
			if execErr != nil || outcome == nil {
				logger.Warn("tool_execute_failed", "tool", call.Name, sdk.LogFieldError, execErr)
				outcome = &toolOutcome{ForModel: "工具执行失败,请基于已有信息作答", IsError: true}
			}
		}
		duration := time.Since(began)

		status := "ok"
		errText := ""
		if outcome.IsError {
			status = "error"
			errText = truncateForModel(outcome.ForModel, 300)
		}
		if outcome.Usage != nil {
			stats.usage = mergeUsage(stats.usage, outcome.Usage)
		}
		_ = writeToolEvent(w, "tool_call_finished", stats.iterations, toolEventCall{
			ID:         call.ID,
			Name:       call.Name,
			Status:     status,
			DurationMs: duration.Milliseconds(),
			Result:     outcome.ForClient,
			Error:      errText,
		})
		p.auditToolCall(tc, stats.iterations, call, status, errText, duration, len(outcome.ForModel))
		results = append(results, toolExecResult{call: call, outcome: outcome})
	}
	return results
}

// auditToolCall 工具执行审计(失败只记日志,不阻断主流程)。
// 用独立短超时 ctx:请求取消后审计仍要落库。
func (p *Plugin) auditToolCall(tc *toolContext, iteration int, call accumToolCall, status, errText string, duration time.Duration, resultBytes int) {
	if p.svc == nil {
		return
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.svc.InsertToolCallAudit(auditCtx, toolCallAudit{
		UserID:         tc.userID,
		ConversationID: tc.conversationID,
		RequestID:      tc.requestID,
		Iteration:      iteration,
		ToolName:       call.Name,
		Arguments:      truncateForModel(string(call.Args), 8<<10),
		Status:         status,
		Error:          errText,
		DurationMs:     duration.Milliseconds(),
		ResultBytes:    resultBytes,
	})
	if err != nil {
		tc.logger.Warn("tool_audit_insert_failed", sdk.LogFieldError, err)
	}
}
