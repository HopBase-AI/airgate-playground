package playground

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const (
	headerEntry  = "X-Airgate-Entry"
	headerUserID = "X-Airgate-User-Id"

	// 转发 body 上限：历史图片资产在 rewriteChatImageAssetURLs 展开成 data URL 后，
	// 多轮会话可能持续膨胀。Anthropic /v1/messages 请求上限 32MB，这里预留余量提前拦截，
	// 避免把注定失败的大请求推给上游。
	maxChatForwardBodyBytes = 30 << 20
)

var errChatBodyTooLarge = errors.New("Conversation payload too large: history images and attachments exceed 30MB after expansion. Remove some images or start a new conversation.")

func validateChatForwardBodySize(size int) error {
	if size > maxChatForwardBodyBytes {
		return errChatBodyTooLarge
	}
	return nil
}

func (p *Plugin) RegisterRoutes(r sdk.RouteRegistrar) {
	// Conversation CRUD
	r.Handle(http.MethodPost, "/conversations", p.requireUser(p.handleCreateConversation))
	r.Handle(http.MethodGet, "/conversations", p.requireUser(p.handleListConversations))
	r.Handle(http.MethodGet, "/conversations/", p.requireUser(p.handleGetConversation))
	r.Handle(http.MethodPut, "/conversations/", p.requireUser(p.handleUpdateConversation))
	r.Handle(http.MethodDelete, "/conversations/", p.requireUser(p.handleDeleteConversation))

	// Messages
	r.Handle(http.MethodGet, "/messages/", p.requireUser(p.handleListMessages))
	r.Handle(http.MethodPut, "/messages/", p.requireUser(p.handleUpdateMessage))
	r.Handle(http.MethodPost, "/messages", p.requireUser(p.handlePersistMessage))
	r.Handle(http.MethodPost, "/exports", p.requireUser(p.handleDirectExport))
	r.Handle(http.MethodPost, "/messages/truncate", p.requireUser(p.handleTruncateMessages))
	r.Handle(http.MethodPost, "/chat/completions", p.requireUser(p.handleChatCompletions))

	// Metadata
	r.Handle(http.MethodGet, "/user/info", p.requireUser(p.handleGetUserInfo))
	r.Handle(http.MethodGet, "/models", p.requireUser(p.handleListModels))
}

// ── Middleware ──

func (p *Plugin) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entry := r.Header.Get(headerEntry)
		if entry != "user" && entry != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "user entry required"})
			return
		}
		if r.Header.Get(headerUserID) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing user identity"})
			return
		}
		if !p.Configured() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugin not configured"})
			return
		}
		next(w, r)
	}
}

// ── Conversation Handlers ──

func (p *Plugin) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	userID := parseUserID(r)
	var req struct {
		Title    string `json:"title"`
		GroupID  int64  `json:"group_id"`
		Platform string `json:"platform"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	conv, err := p.svc.CreateConversation(r.Context(), userID, req.Title, req.GroupID, req.Platform, req.Model)
	if err != nil {
		var limitErr *conversationLimitError
		if errors.As(err, &limitErr) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, conv)
}

func (p *Plugin) handleListConversations(w http.ResponseWriter, r *http.Request) {
	convs, err := p.svc.ListConversations(r.Context(), parseUserID(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if convs == nil {
		convs = []Conversation{}
	}
	writeJSON(w, http.StatusOK, convs)
}

func (p *Plugin) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	convID := parsePathID(r.URL.Path, "/conversations/")
	if convID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
		return
	}
	conv, err := p.svc.GetConversation(r.Context(), parseUserID(r), convID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if conv == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "conversation not found"})
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (p *Plugin) handleUpdateConversation(w http.ResponseWriter, r *http.Request) {
	convID := parsePathID(r.URL.Path, "/conversations/")
	if convID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
		return
	}
	var req struct {
		Title    string `json:"title"`
		GroupID  int64  `json:"group_id"`
		Platform string `json:"platform"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := p.svc.UpdateConversation(r.Context(), parseUserID(r), convID, req.Title, req.GroupID, req.Platform, req.Model); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (p *Plugin) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	convID := parsePathID(r.URL.Path, "/conversations/")
	if convID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
		return
	}
	if err := p.svc.DeleteConversation(r.Context(), parseUserID(r), convID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Message Handler ──

func (p *Plugin) handleListMessages(w http.ResponseWriter, r *http.Request) {
	convID := parsePathID(r.URL.Path, "/messages/")
	if convID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid conversation id"})
		return
	}
	msgs, err := p.svc.ListMessages(r.Context(), parseUserID(r), convID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if msgs == nil {
		msgs = []Message{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// handleTruncateMessages 线性截断会话(编辑重发前置步骤):删除
// after_message_id 之后的所有消息。
func (p *Plugin) handleTruncateMessages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID int64 `json:"conversation_id"`
		AfterMessageID int64 `json:"after_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	deleted, err := p.svc.TruncateMessages(r.Context(), parseUserID(r), req.ConversationID, req.AfterMessageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (p *Plugin) handlePersistMessage(w http.ResponseWriter, r *http.Request) {
	var req PersistMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ConversationID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id required"})
		return
	}
	if req.Role == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role required"})
		return
	}

	msg, err := p.svc.PersistMessage(r.Context(), parseUserID(r), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

// handleDirectExport renders an already-persisted message without calling an
// upstream model. The response explicitly reports model_tokens=0 so clients
// can distinguish this path from AI-authored generation.
func (p *Plugin) handleDirectExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID int64  `json:"message_id"`
		Format    string `json:"format"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if req.MessageID <= 0 || !isDirectExportFormat(format) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_id and format(pdf, docx, pptx, xlsx) are required"})
		return
	}
	if !p.directExportEnabled(format) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": format + " export is not enabled"})
		return
	}
	message, conversation, err := p.svc.GetMessageForExport(r.Context(), parseUserID(r), req.MessageID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	title := sanitizeDocumentTitle(req.Title)
	if strings.TrimSpace(req.Title) == "" {
		title = sanitizeDocumentTitle(conversation.Title)
	}
	content := stripDuplicateLeadingTitle(title, message.Content)
	var data []byte
	switch format {
	case "pdf":
		if p.pdf == nil || !p.pdf.Healthy(r.Context()) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "PDF renderer unavailable"})
			return
		}
		html, renderErr := renderDocumentHTML(title, []byte(content))
		if renderErr == nil {
			data, renderErr = p.pdf.RenderPDF(r.Context(), html)
		}
		if renderErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "PDF export failed: " + renderErr.Error()})
			return
		}
	case "docx":
		if p.office == nil || !p.office.Healthy(r.Context()) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Office renderer unavailable"})
			return
		}
		data, err = p.office.RenderDOCX(r.Context(), title, content)
	case "xlsx":
		input, parseErr := markdownToSpreadsheet(title, content)
		if parseErr == nil {
			data, parseErr = renderSpreadsheet(input)
		}
		err = parseErr
	case "pptx":
		if p.office == nil || !p.office.Healthy(r.Context()) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Office renderer unavailable"})
			return
		}
		input, parseErr := markdownToPresentation(title, content)
		if parseErr == nil {
			data, parseErr = p.office.RenderPPTX(r.Context(), input)
		}
		err = parseErr
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": format + " export failed: " + err.Error()})
		return
	}
	maxSize := int64(maxDocumentPDFBytes)
	contentType, extension := "application/pdf", ".pdf"
	switch format {
	case "docx":
		maxSize = maxDocumentDOCXBytes
		contentType, extension = "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"
	case "pptx":
		maxSize = maxOfficeRendererResponseBytes
		contentType, extension = "application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"
	case "xlsx":
		maxSize = maxSpreadsheetBytes
		contentType, extension = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"
	}
	if int64(len(data)) > maxSize {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": format + " export exceeds size limit"})
		return
	}
	asset, err := p.svc.Storage().StoreDocumentBytes(r.Context(), parseUserID(r), contentType, extension, data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "export asset save failed"})
		return
	}
	if err := p.svc.RegisterConversationAsset(r.Context(), parseUserID(r), conversation.ID, asset); err != nil {
		_ = p.svc.Storage().Delete(r.Context(), asset.ObjectKey)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "export asset registration failed"})
		return
	}
	tc := &toolContext{userID: int64(parseUserID(r)), conversationID: conversation.ID, requestID: sdk.ExtractOrGenerateRequestID(r.Header), logger: p.logger}
	usage, err := p.chargeRenderUsage(r.Context(), tc, format, asset.ID, asset.SizeBytes, 1)
	if err != nil {
		_ = p.svc.RemoveConversationAsset(r.Context(), parseUserID(r), conversation.ID, asset)
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "export render fee could not be charged: " + err.Error()})
		return
	}
	response := map[string]any{
		"file": map[string]any{
			"name": title + extension, "content_type": contentType, "size": asset.SizeBytes,
			"src": asset.PublicURL, "asset_uri": assetURI(asset.ID),
		},
		"model_tokens": 0,
		"render_fee":   0.0,
	}
	if usage != nil {
		response["usage"] = usage
		response["render_fee"] = usage.UserCost
	}
	writeJSON(w, http.StatusCreated, response)
}

func isDirectExportFormat(format string) bool {
	switch format {
	case "pdf", "docx", "pptx", "xlsx":
		return true
	default:
		return false
	}
}

func (p *Plugin) directExportEnabled(format string) bool {
	settings := p.toolSettingsValue()
	switch format {
	case "pdf":
		return settings.GenerateDocumentEnabled && p.pdf != nil
	case "docx", "pptx":
		return settings.GenerateOfficeEnabled && p.office != nil
	case "xlsx":
		return settings.GenerateSpreadsheetEnabled
	default:
		return false
	}
}

func (p *Plugin) handleUpdateMessage(w http.ResponseWriter, r *http.Request) {
	msgID := parsePathID(r.URL.Path, "/messages/")
	if msgID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message id"})
		return
	}

	var req UpdateMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	msg, err := p.svc.UpdateMessage(r.Context(), parseUserID(r), msgID, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (p *Plugin) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// 派生请求级 logger 写回 ctx
	rid := sdk.ExtractOrGenerateRequestID(r.Header)
	ctx := sdk.WithRequestID(r.Context(), rid)
	ctx, logger := sdk.LoggerWithRequestID(ctx)
	logger.Debug("playground_request_received",
		sdk.LogFieldMethod, r.Method,
		sdk.LogFieldPath, r.URL.Path,
		sdk.LogFieldUserID, parseUserID(r),
	)

	platform := strings.TrimSpace(r.Header.Get("X-Airgate-Platform"))
	if platform == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "platform required")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "invalid request body")
		return
	}
	// 资产重写只会让 body 变大（asset 引用 → base64 data URL），原始超限的请求
	// 先拒掉，省去整份 JSON 解析、资产拉取与 base64 编码。
	if err := validateChatForwardBodySize(len(body)); err != nil {
		logger.Warn("chat_forward_body_too_large", "body_bytes", len(body), "stage", "raw")
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "request_too_large", err.Error())
		return
	}
	body, err = p.rewriteChatImageAssetURLs(ctx, body)
	if err != nil {
		logger.Warn("chat_asset_rewrite_failed", sdk.LogFieldError, err)
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error())
		return
	}
	if err := validateChatForwardBodySize(len(body)); err != nil {
		logger.Warn("chat_forward_body_too_large", "body_bytes", len(body), "stage", "expanded")
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "request_too_large", err.Error())
		return
	}
	opts := defaultCompileOpts()
	opts.Tuning = p.chatTuningValue()
	if lp := strings.ToLower(platform); lp == "claude" || lp == "anthropic" {
		opts.LookupMaxOutput = func(model string) (int, bool) {
			return p.lookupModelMaxOutput(ctx, "claude", model)
		}
	}
	plan, err := compileChatForwardPlanWithOpts(platform, body, opts)
	if err != nil {
		logger.Warn("chat_forward_compile_failed", sdk.LogFieldPlatform, platform, sdk.LogFieldError, err)
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error())
		return
	}
	// degradedPlan:上游 400 且错误指向 thinking/output_config/cache_control 时,
	// 剥离对应字段重编译(防号池/中转不透传新字段),每请求至多重试一次。
	degradedPlan := func(failBody []byte) *chatForwardPlan {
		if plan.Platform != "claude" || opts.DisableThinking || opts.DisableCache {
			return nil
		}
		dropThinking, dropCache := chatDegradeFlags(failBody)
		if !dropThinking && !dropCache {
			return nil
		}
		opts.DisableThinking = dropThinking
		opts.DisableCache = dropCache
		retryPlan, retryErr := compileChatForwardPlanWithOpts(platform, body, opts)
		if retryErr != nil {
			return nil
		}
		logger.Warn("chat_claude_degraded_retry",
			"drop_thinking", dropThinking,
			"drop_cache", dropCache,
			sdk.LogFieldModel, plan.Model,
		)
		return retryPlan
	}
	var fields struct {
		Stream *bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &fields)
	stream := fields.Stream == nil || *fields.Stream

	// 工具循环:开关开启且流式请求时接管(claude/openai 双协议)。
	if stream {
		if tools := p.enabledChatTools(); len(tools) > 0 {
			// gemini 不进工具循环:gemini 插件伪流式(内容可能一次性到达)且首期
			// 未验证 tool_calls 透传,web_search 等服务端工具暂不覆盖,走普通转发。
			if lp := strings.ToLower(platform); lp == "claude" || lp == "anthropic" || lp == "openai" {
				var loopReq openAIChatRequest
				if err := json.Unmarshal(body, &loopReq); err != nil {
					writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "invalid request body")
					return
				}
				loopReq.Model = strings.TrimSpace(loopReq.Model)
				if loopReq.Model == "" {
					writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "model required")
					return
				}
				p.runToolLoop(ctx, w, r, lp, loopReq, body, opts, tools, logger)
				return
			}
		}
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Airgate-Platform", plan.Platform)
	logger.Debug("upstream_request_start",
		sdk.LogFieldPlatform, plan.Platform,
		sdk.LogFieldModel, plan.Model,
		sdk.LogFieldPath, plan.Path,
		"stream", stream,
	)
	if !stream {
		headers.Set("Accept", "application/json")
		doForward := func(fwdPlan *chatForwardPlan) (*hostForwardResponse, error) {
			return hostForward(ctx, p.host, hostForwardRequest{
				UserID:  int64(parseUserID(r)),
				GroupID: 0,
				Model:   fwdPlan.Model,
				Method:  http.MethodPost,
				Path:    fwdPlan.Path,
				Headers: headers,
				Body:    fwdPlan.Body,
				Stream:  false,
			})
		}
		resp, err := doForward(plan)
		if err == nil && resp.StatusCode == http.StatusBadRequest {
			if retryPlan := degradedPlan(resp.Body); retryPlan != nil {
				if resp2, err2 := doForward(retryPlan); err2 == nil {
					plan = retryPlan
					resp = resp2
				}
			}
		}
		if err != nil {
			logger.Warn("upstream_request_failed",
				sdk.LogFieldPlatform, plan.Platform,
				sdk.LogFieldModel, plan.Model,
				sdk.LogFieldError, err,
			)
			p.writeChatForwardError(ctx, w, err, int64(parseUserID(r)), plan.Platform)
			return
		}
		logger.Debug("upstream_request_completed",
			sdk.LogFieldPlatform, plan.Platform,
			sdk.LogFieldModel, plan.Model,
			sdk.LogFieldStatus, resp.StatusCode,
		)
		copyHeaders(w.Header(), resp.Headers)
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		status := resp.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		respBody := resp.Body
		if plan.NormalizeJSON && status >= 200 && status < 300 {
			if normalized, err := normalizeClaudeMessageResponse(respBody); err == nil {
				respBody = normalized
			}
		}
		_, _ = w.Write(respBody)
		return
	}
	headers.Set("Accept", "text/event-stream")

	// streamAttempt 执行一次流式转发。上游非 2xx 时不向客户端提交,缓冲错误体
	// 返回(供 400 降级重试判定);2xx 正常边收边写。
	type streamAttemptResult struct {
		committed  bool
		failStatus int
		failHeader http.Header
		failBody   []byte
		usage      *sdk.Usage
		err        error
	}
	const maxStreamFailBodyBytes = 64 << 10
	streamAttempt := func(fwdPlan *chatForwardPlan) streamAttemptResult {
		res := streamAttemptResult{}
		var bridge *claudeSSEBridge
		if fwdPlan.NormalizeSSE {
			bridge = &claudeSSEBridge{w: w, model: fwdPlan.ResponseModel}
		}
		res.err = hostForwardStream(ctx, p.host, hostForwardRequest{
			UserID:  int64(parseUserID(r)),
			GroupID: 0,
			Model:   fwdPlan.Model,
			Method:  http.MethodPost,
			Path:    fwdPlan.Path,
			Headers: headers,
			Body:    fwdPlan.Body,
			Stream:  true,
		}, func(chunk hostForwardChunk) error {
			if chunk.Done {
				res.usage = chunk.Usage
				if bridge != nil && res.committed {
					return bridge.Flush()
				}
				return nil
			}
			if !res.committed && res.failStatus == 0 {
				status := chunk.StatusCode
				if status == 0 {
					status = http.StatusOK
				}
				if status >= http.StatusBadRequest {
					res.failStatus = status
					res.failHeader = chunk.Headers
				} else {
					copyStreamingHeaders(w.Header(), chunk.Headers, fwdPlan.NormalizeSSE)
					if w.Header().Get("Content-Type") == "" {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					w.WriteHeader(status)
					res.committed = true
				}
			}
			if res.failStatus > 0 {
				if len(chunk.Data) > 0 && len(res.failBody) < maxStreamFailBodyBytes {
					res.failBody = append(res.failBody, chunk.Data...)
				}
				return nil
			}
			if len(chunk.Data) > 0 {
				if bridge != nil {
					if _, err := bridge.Write(chunk.Data); err != nil {
						return err
					}
				} else {
					if _, err := w.Write(chunk.Data); err != nil {
						return err
					}
				}
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return nil
		})
		return res
	}

	res := streamAttempt(plan)
	if res.err == nil && res.failStatus == http.StatusBadRequest {
		if retryPlan := degradedPlan(res.failBody); retryPlan != nil {
			plan = retryPlan
			res = streamAttempt(retryPlan)
		}
	}
	if res.err != nil {
		logger.Warn("upstream_request_failed",
			sdk.LogFieldPlatform, plan.Platform,
			sdk.LogFieldModel, plan.Model,
			"stream", true,
			sdk.LogFieldError, res.err,
		)
		if !res.committed {
			p.writeChatForwardError(ctx, w, res.err, int64(parseUserID(r)), plan.Platform)
			return
		}
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"The request could not be completed. Please try again later.\",\"type\":\"server_error\",\"code\":\"upstream_error\"}}\n\n"))
		return
	}
	if res.failStatus > 0 {
		// 上游错误体原样回给客户端(未提交过任何字节,可以完整改写响应)。
		logger.Warn("upstream_request_failed",
			sdk.LogFieldPlatform, plan.Platform,
			sdk.LogFieldModel, plan.Model,
			"stream", true,
			sdk.LogFieldStatus, res.failStatus,
		)
		copyHeaders(w.Header(), res.failHeader)
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(res.failStatus)
		_, _ = w.Write(res.failBody)
		return
	}
	logger.Debug("upstream_request_completed",
		sdk.LogFieldPlatform, plan.Platform,
		sdk.LogFieldModel, plan.Model,
		"stream", true,
	)
	if res.usage != nil {
		if plan.NormalizeSSE {
			_ = writeOpenAIUsage(w, res.usage, res.usage.Model)
		} else {
			payload, _ := json.Marshal(map[string]any{"usage": res.usage})
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(payload)
			_, _ = w.Write([]byte("\n\n"))
		}
	}
}

// ── Metadata Handlers ──

func (p *Plugin) handleGetUserInfo(w http.ResponseWriter, r *http.Request) {
	info, err := hostGetUserInfo(r.Context(), p.host, int64(parseUserID(r)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// chatModelPlatforms AI Chat 支持的平台，顺序即前端下拉的分组顺序。
// 模型清单来自各网关插件的注册表（claude＝Max 号池网关声明的模型；
// 转发始终走用户 API Key 所属分组，不会因选择模型改路由到别的分组）。
var chatModelPlatforms = []string{"claude", "openai", "gemini"}

func (p *Plugin) handleListModels(w http.ResponseWriter, r *http.Request) {
	// 企业成员的分组白名单可能把某平台的分组全排除，此时该平台的模型对他根本路由不到，
	// 不能出现在下拉里（否则发消息时 core 自动选组零候选，只能报通用错误）。
	eligible := p.resolvePlatformEligibility(r.Context(), int64(parseUserID(r)), chatModelPlatforms)
	models := make([]hostModelInfo, 0, 16)
	for _, platform := range chatModelPlatforms {
		items, err := hostListModels(r.Context(), p.host, platform)
		if err != nil {
			// 单平台失败不阻断（如插件未装）；前端拿到空列表时回退硬编码
			continue
		}
		for _, item := range items {
			// 只放对话模型：同平台注册表里可能混有图像生成模型（image_generation），
			// 不应出现在 AI Chat 的模型下拉里
			if modelSupportsChat(item.Capabilities) {
				models = append(models, item)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models": filterChatModelsByEligibility(models, eligible),
		// 前端用它区分"平台被权限裁掉"与"平台目录暂时拉不到"：后者才用硬编码兜底补齐
		"eligible_platforms": eligible.allowedPlatforms(chatModelPlatforms),
	})
}

// platformEligibility 用户在各平台的转发资格：true＝至少有一个可用分组；false＝一个都没有
// （典型：企业成员的分组白名单把该平台的分组全排除）；查询失败的平台不入表，视为未知放行——
// 资格查询出问题绝不能让所有人的 AI Chat 没模型可选。
type platformEligibility map[string]bool

// resolvePlatformEligibility 逐平台经 groups.list(eligible_only) 判定资格；host 不可用或拿不到
// 用户身份时返回空表（全部放行，即旧行为）。
func (p *Plugin) resolvePlatformEligibility(ctx context.Context, userID int64, platforms []string) platformEligibility {
	eligible := platformEligibility{}
	if p.host == nil || userID <= 0 {
		return eligible
	}
	for _, platform := range platforms {
		count, err := hostCountEligibleGroups(ctx, p.host, userID, platform)
		if err != nil {
			if p.logger != nil {
				p.logger.Warn("chat_platform_eligibility_query_failed",
					sdk.LogFieldPlatform, platform, sdk.LogFieldUserID, userID, sdk.LogFieldError, err)
			}
			continue
		}
		eligible[strings.ToLower(platform)] = count > 0
	}
	return eligible
}

// allowedPlatforms 按给定顺序返回未被明确判为无资格的平台（未知视为有资格）。
func (e platformEligibility) allowedPlatforms(platforms []string) []string {
	out := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		if allowed, known := e[strings.ToLower(platform)]; known && !allowed {
			continue
		}
		out = append(out, platform)
	}
	return out
}

// filterChatModelsByEligibility 裁掉用户明确无资格平台的模型；资格未知的平台原样保留。
func filterChatModelsByEligibility(models []hostModelInfo, eligible platformEligibility) []hostModelInfo {
	out := make([]hostModelInfo, 0, len(models))
	for _, m := range models {
		if allowed, known := eligible[strings.ToLower(m.Platform)]; known && !allowed {
			continue
		}
		out = append(out, m)
	}
	return out
}

// modelSupportsChat 判断模型是否可用于对话。空 capabilities 视为对话模型（历史/兜底），
// 显式声明时必须含 "chat"。
func modelSupportsChat(capabilities []string) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, c := range capabilities {
		if strings.EqualFold(c, "chat") {
			return true
		}
	}
	return false
}

// ── Helpers ──

func parseUserID(r *http.Request) int {
	id, _ := strconv.Atoi(r.Header.Get(headerUserID))
	return id
}

func parsePathID(path, prefix string) int64 {
	idx := strings.LastIndex(path, prefix)
	if idx < 0 {
		return 0
	}
	seg := strings.TrimRight(path[idx+len(prefix):], "/")
	id, _ := strconv.ParseInt(seg, 10, 64)
	return id
}

func (p *Plugin) rewriteChatImageAssetURLs(ctx context.Context, body []byte) ([]byte, error) {
	if p.svc == nil || p.svc.storage == nil || !bytes.Contains(body, []byte(`"image_url"`)) || !bytes.Contains(body, []byte("/assets-runtime/")) {
		return body, nil
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, nil
	}
	changed := false
	converted, err := p.rewriteImageURLValue(ctx, payload, &changed)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(converted)
}

func (p *Plugin) rewriteImageURLValue(ctx context.Context, value any, changed *bool) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		if raw, ok := v["url"].(string); ok && strings.HasPrefix(raw, "/assets-runtime/") {
			dataURL, err := p.assetRuntimeURLToDataURL(ctx, raw)
			if err != nil {
				return nil, err
			}
			v["url"] = dataURL
			*changed = true
		}
		for key, item := range v {
			converted, err := p.rewriteImageURLValue(ctx, item, changed)
			if err != nil {
				return nil, err
			}
			v[key] = converted
		}
		return v, nil
	case []any:
		for i, item := range v {
			converted, err := p.rewriteImageURLValue(ctx, item, changed)
			if err != nil {
				return nil, err
			}
			v[i] = converted
		}
		return v, nil
	default:
		return value, nil
	}
}

func (p *Plugin) assetRuntimeURLToDataURL(ctx context.Context, raw string) (string, error) {
	objectKey, err := assetObjectKeyFromRuntimeURL(raw)
	if err != nil {
		return "", err
	}
	asset, err := p.svc.storage.GetBytes(ctx, objectKey)
	if err != nil {
		return "", err
	}
	contentType := strings.TrimSpace(asset.ContentType)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = imageContentTypeForObjectKey(objectKey)
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(asset.Data), nil
}

func assetObjectKeyFromRuntimeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	pathValue := u.Path
	if pathValue == "" && strings.HasPrefix(raw, "/assets-runtime/") {
		pathValue = raw
	}
	const prefix = "/assets-runtime/"
	if !strings.HasPrefix(pathValue, prefix) {
		return "", fmt.Errorf("invalid asset url")
	}
	objectKey, err := url.PathUnescape(strings.TrimPrefix(pathValue, prefix))
	if err != nil {
		return "", err
	}
	objectKey = strings.TrimLeft(objectKey, "/")
	if objectKey == "" {
		return "", fmt.Errorf("invalid asset url")
	}
	return objectKey, nil
}

func imageContentTypeForObjectKey(objectKey string) string {
	switch {
	case strings.HasSuffix(strings.ToLower(objectKey), ".jpg"), strings.HasSuffix(strings.ToLower(objectKey), ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(strings.ToLower(objectKey), ".png"):
		return "image/png"
	case strings.HasSuffix(strings.ToLower(objectKey), ".webp"):
		return "image/webp"
	case strings.HasSuffix(strings.ToLower(objectKey), ".gif"):
		return "image/gif"
	case strings.HasSuffix(strings.ToLower(objectKey), ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(strings.ToLower(objectKey), ".mov"):
		return "video/quicktime"
	case strings.HasSuffix(strings.ToLower(objectKey), ".webm"):
		return "video/webm"
	case strings.HasSuffix(strings.ToLower(objectKey), ".m4v"):
		return "video/x-m4v"
	default:
		return "image/png"
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, errType, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

// 客户可见文案一律英文：AI Chat 面向多语言用户，中文文案会直接漏给西语/英语客户。
// 本地化由 core 按 Accept-Language + error code 完成，插件只产英文。
const (
	// chatErrMemberGroupForbidden 企业成员选了白名单外分组才能路由到的模型时的用户提示。
	chatErrMemberGroupForbidden = "Your organization administrator has not granted access to this model. Contact your administrator or pick another model."
	// chatErrUpstreamUnavailable 上游不可用/未知错误时的通用提示。
	chatErrUpstreamUnavailable = "The request could not be completed. Please try again later."
	// chatErrInvalidRequest core 判定参数非法但没有给出具体原因时的兜底提示。
	chatErrInvalidRequest = "The request could not be completed. Check the request parameters and try again."
	// chatErrInsufficientBalance 余额不足。
	chatErrInsufficientBalance = "Insufficient balance."
)

// memberGroupForbiddenHints core 拒绝成员使用白名单外分组时的错误文案片段。
// 插件不能 import core，只能按文案识别，所以这里是**判别串**而非对外文案——
// 它们必须跟着 core 的实际输出走，不能按"对外一律英文"的纪律去改。
//
// 两条都要留：core 2026-09-10 起把该错误改成英文（gw.member_group_forbidden＝
// "This member is not allowed to use the group bound to this API key"），
// 但线上回滚到旧 core 时仍是中文（auth.ErrMemberGroupForbidden＝"所属团队成员无权使用该分组"）。
// 只认其中一条，另一形态就会退化成通用 upstream_error，成员分组白名单的拒绝理由丢失。
var memberGroupForbiddenHints = []string{
	"not allowed to use the group",
	"无权使用该分组",
}

// isMemberGroupForbiddenError 判断 core 是否因成员分组白名单显式拒绝了转发（PermissionDenied）。
func isMemberGroupForbiddenError(err error) bool {
	s, ok := status.FromError(err)
	if !ok || s.Code() != codes.PermissionDenied {
		return false
	}
	for _, hint := range memberGroupForbiddenHints {
		if strings.Contains(s.Message(), hint) {
			return true
		}
	}
	return false
}

// forwardErrorIsMemberGroupForbidden 把两种形态的"成员无权用该模型"归一：
//  1. core 显式拒绝（PermissionDenied + 白名单文案）；
//  2. 自动选组时白名单把候选分组全滤掉——core 只返回通用 Unavailable，从错误本身分不出是上游故障
//     还是没有可用分组，这里追问一次 groups.list(eligible_only)：该平台确无任何可用分组才定性为权限问题。
func (p *Plugin) forwardErrorIsMemberGroupForbidden(ctx context.Context, err error, userID int64, platform string) bool {
	if isMemberGroupForbiddenError(err) {
		return true
	}
	s, ok := status.FromError(err)
	if !ok || s.Code() != codes.Unavailable || p.host == nil || userID <= 0 || strings.TrimSpace(platform) == "" {
		return false
	}
	count, qerr := hostCountEligibleGroups(ctx, p.host, userID, platform)
	return qerr == nil && count == 0
}

// writeChatForwardError 在通用映射之前先识别成员分组白名单拒绝，给出可操作的提示。
func (p *Plugin) writeChatForwardError(ctx context.Context, w http.ResponseWriter, err error, userID int64, platform string) {
	if p.forwardErrorIsMemberGroupForbidden(ctx, err, userID, platform) {
		writeOpenAIError(w, http.StatusForbidden, "permission_error", "member_group_forbidden", chatErrMemberGroupForbidden)
		return
	}
	writeHostForwardError(w, err)
}

func writeHostForwardError(w http.ResponseWriter, err error) {
	s, ok := status.FromError(err)
	if !ok {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "upstream_error", chatErrUpstreamUnavailable)
		return
	}
	switch s.Code() {
	case codes.InvalidArgument:
		msg := s.Message()
		if msg == "" {
			msg = chatErrInvalidRequest
		}
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", msg)
	case codes.ResourceExhausted:
		writeOpenAIError(w, http.StatusPaymentRequired, "insufficient_quota", "insufficient_quota", chatErrInsufficientBalance)
	default:
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "upstream_error", chatErrUpstreamUnavailable)
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}
