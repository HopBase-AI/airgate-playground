package playground

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const (
	hostMethodGatewayForward = "gateway.forward"
	hostMethodUsersGet       = "users.get"
	hostMethodModelsList     = "models.list"
	hostMethodGroupsList     = "groups.list"
	hostMethodAssetsStore    = "assets.store"
	hostMethodAssetsGetURL   = "assets.get_url"
	hostMethodAssetsGetBytes = "assets.get_bytes"
	hostMethodAssetsDelete   = "assets.delete"
	hostMethodUsageRecord    = "usage.record"
)

// hostModelInfo 是 models.list 返回的单个模型条目（转发给前端选择器）。
type hostModelInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Platform        string   `json:"platform"`
	ContextWindow   int64    `json:"context_window"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	Capabilities    []string `json:"capabilities"`
}

// hostListModels 拉取指定平台的模型列表（来源＝对应网关插件声明的注册表 + 后台目录覆盖层）。
func hostListModels(ctx context.Context, host sdk.Host, platform string) ([]hostModelInfo, error) {
	result, err := hostInvoke(ctx, host, hostMethodModelsList, map[string]interface{}{
		"platform": platform,
	})
	if err != nil {
		return nil, err
	}
	rawItems, _ := result["models"].([]interface{})
	models := make([]hostModelInfo, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		info := hostModelInfo{Platform: platform}
		info.ID, _ = item["id"].(string)
		info.Name, _ = item["name"].(string)
		if v, ok := item["context_window"].(float64); ok {
			info.ContextWindow = int64(v)
		}
		if v, ok := item["max_output_tokens"].(float64); ok {
			info.MaxOutputTokens = int64(v)
		}
		if caps, ok := item["capabilities"].([]interface{}); ok {
			for _, c := range caps {
				if s, ok := c.(string); ok {
					info.Capabilities = append(info.Capabilities, s)
				}
			}
		}
		if info.ID != "" {
			models = append(models, info)
		}
	}
	return models, nil
}

// hostCountEligibleGroups 查询用户在某平台下有转发资格的分组数（groups.list 的 eligible_only 分支）。
// 资格判定全在 core：成员账号按企业主判定、再按成员分组白名单收敛，与 gateway.forward
// 未指定 group_id 时的自动选组口径一致——为 0 即意味着该平台的任何模型都路由不到。
func hostCountEligibleGroups(ctx context.Context, host sdk.Host, userID int64, platform string) (int, error) {
	result, err := hostInvoke(ctx, host, hostMethodGroupsList, map[string]interface{}{
		"user_id":       userID,
		"eligible_only": true,
		"platform":      platform,
	})
	if err != nil {
		return 0, err
	}
	groups, _ := result["groups"].([]interface{})
	return len(groups), nil
}

type hostForwardRequest struct {
	UserID    int64
	GroupID   int64
	RequestID string
	TraceID   string
	Model     string
	Method    string
	Path      string
	Headers   http.Header
	Body      []byte
	Stream    bool
}

type hostForwardResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Usage      *sdk.Usage
	UsageID    int
}

type hostForwardChunk struct {
	Done       bool
	StatusCode int
	Headers    http.Header
	Data       []byte
	Usage      *sdk.Usage
	UsageID    int
}

type hostAssetBytes struct {
	Data        []byte
	ContentType string
}

func hostInvoke(ctx context.Context, host sdk.Host, method string, payload map[string]interface{}) (map[string]interface{}, error) {
	return hostInvokeWithOptions(ctx, host, method, payload, "", nil)
}

func hostInvokeWithOptions(ctx context.Context, host sdk.Host, method string, payload map[string]interface{}, idempotencyKey string, metadata map[string]string) (map[string]interface{}, error) {
	if host == nil {
		return nil, fmt.Errorf("core host is not enabled")
	}
	resp, err := host.Invoke(ctx, sdk.HostInvokeRequest{
		Method:         method,
		Payload:        payload,
		IdempotencyKey: idempotencyKey,
		Metadata:       metadata,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return map[string]interface{}{}, nil
	}
	if strings.EqualFold(resp.Status, "error") {
		if msg, _ := resp.Payload["message"].(string); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, fmt.Errorf("core method %s returned an error", method)
	}
	return resp.Payload, nil
}

func hostRecordUsage(ctx context.Context, host sdk.Host, payload map[string]interface{}, idempotencyKey string) (*sdk.Usage, error) {
	result, err := hostInvokeWithOptions(ctx, host, hostMethodUsageRecord, payload, idempotencyKey, nil)
	if err != nil {
		return nil, err
	}
	usage := usageFromPayload(firstPayloadValue(result, "usage"))
	return usage, nil
}

func hostForward(ctx context.Context, host sdk.Host, req hostForwardRequest) (*hostForwardResponse, error) {
	payload, err := hostInvoke(ctx, host, hostMethodGatewayForward, hostForwardPayload(req))
	if err != nil {
		return nil, err
	}
	return &hostForwardResponse{
		StatusCode: intFromAny(firstPayloadValue(payload, "status_code", "status")),
		Headers:    headerFromPayload(firstPayloadValue(payload, "headers")),
		Body:       bytesFromPayload(firstPayloadValue(payload, "body")),
		Usage:      usageFromPayload(firstPayloadValue(payload, "usage")),
		UsageID:    intFromAny(firstPayloadValue(payload, "usage_id")),
	}, nil
}

func hostForwardStream(ctx context.Context, host sdk.Host, req hostForwardRequest, onChunk func(hostForwardChunk) error) error {
	if host == nil {
		return fmt.Errorf("core host is not enabled")
	}
	req.Stream = true
	stream, err := host.InvokeStream(ctx, sdk.HostStreamRequest{
		Method:  hostMethodGatewayForward,
		Payload: hostForwardPayload(req),
	})
	if err != nil {
		return err
	}
	defer func() { _ = stream.CloseSend() }()

	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		payload := frame.Payload
		chunk := hostForwardChunk{
			Done:       frame.Done,
			StatusCode: intFromAny(firstPayloadValue(payload, "status_code", "status")),
			Headers:    headerFromPayload(firstPayloadValue(payload, "headers")),
			Data:       bytesFromPayload(firstPayloadValue(payload, "data")),
			Usage:      usageFromPayload(firstPayloadValue(payload, "usage")),
			UsageID:    intFromAny(firstPayloadValue(payload, "usage_id")),
		}
		if err := onChunk(chunk); err != nil {
			return err
		}
	}
}

func hostForwardPayload(req hostForwardRequest) map[string]interface{} {
	return map[string]interface{}{
		"user_id":    req.UserID,
		"group_id":   req.GroupID,
		"request_id": req.RequestID,
		"trace_id":   req.TraceID,
		"model":      req.Model,
		"method":     req.Method,
		"path":       req.Path,
		"headers":    headerPayload(req.Headers),
		"body":       string(req.Body),
		"stream":     req.Stream,
	}
}

func hostGetUserInfo(ctx context.Context, host sdk.Host, userID int64) (map[string]interface{}, error) {
	return hostInvoke(ctx, host, hostMethodUsersGet, map[string]interface{}{"user_id": userID})
}

func hostStoreAsset(ctx context.Context, host sdk.Host, userID int64, purpose, contentType, fileExtension string, data []byte) (*StoredAsset, error) {
	payload, err := hostInvoke(ctx, host, hostMethodAssetsStore, map[string]interface{}{
		"user_id":        userID,
		"purpose":        purpose,
		"content_type":   contentType,
		"file_extension": fileExtension,
		"data":           data,
	})
	if err != nil {
		return nil, err
	}
	return &StoredAsset{
		ID:          stringFromAny(firstPayloadValue(payload, "asset_id", "id")),
		ObjectKey:   stringFromAny(firstPayloadValue(payload, "object_key")),
		PublicURL:   stringFromAny(firstPayloadValue(payload, "public_url")),
		ContentType: stringFromAny(firstPayloadValue(payload, "content_type")),
		SizeBytes:   int64FromAny(firstPayloadValue(payload, "size_bytes")),
	}, nil
}

func hostGetAssetURL(ctx context.Context, host sdk.Host, objectKey string) (string, error) {
	payload, err := hostInvoke(ctx, host, hostMethodAssetsGetURL, map[string]interface{}{"object_key": objectKey})
	if err != nil {
		return "", err
	}
	return stringFromAny(firstPayloadValue(payload, "public_url", "url")), nil
}

func hostGetAssetBytes(ctx context.Context, host sdk.Host, objectKey string) (*hostAssetBytes, error) {
	payload, err := hostInvoke(ctx, host, hostMethodAssetsGetBytes, map[string]interface{}{"object_key": objectKey})
	if err != nil {
		return nil, err
	}
	return &hostAssetBytes{
		Data:        binaryFromPayload(firstPayloadValue(payload, "data")),
		ContentType: stringFromAny(firstPayloadValue(payload, "content_type")),
	}, nil
}

func hostDeleteAsset(ctx context.Context, host sdk.Host, objectKey string) error {
	_, err := hostInvoke(ctx, host, hostMethodAssetsDelete, map[string]interface{}{"object_key": objectKey})
	return err
}

func firstPayloadValue(payload map[string]interface{}, keys ...string) interface{} {
	if payload == nil {
		return nil
	}
	for _, key := range keys {
		if key == "" {
			return payload
		}
		if value, ok := payload[key]; ok {
			return value
		}
	}
	return nil
}

func headerPayload(headers http.Header) map[string]interface{} {
	out := make(map[string]interface{}, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func headerFromPayload(value interface{}) http.Header {
	headers := http.Header{}
	m, ok := mapFromAny(value)
	if !ok {
		return headers
	}
	for key, raw := range m {
		switch values := raw.(type) {
		case []interface{}:
			for _, item := range values {
				headers.Add(key, stringFromAny(item))
			}
		case []string:
			for _, item := range values {
				headers.Add(key, item)
			}
		default:
			if s := stringFromAny(values); s != "" {
				headers.Set(key, s)
			}
		}
	}
	return headers
}

func bytesFromPayload(value interface{}) []byte {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return v
	case string:
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil && looksLikeJSON(decoded) {
			return decoded
		}
		return []byte(v)
	default:
		body, _ := json.Marshal(v)
		return body
	}
}

// binaryFromPayload 解码 Host.Invoke 载荷中按契约必为二进制的字段（如 assets.get_bytes 的 data）。
// core 侧以 []byte 写入 payload，经 JSON 编组到达插件时必然是 base64 字符串，这里必须无条件解码。
// 不能复用 bytesFromPayload：其 looksLikeJSON 门槛是为 gateway.forward 的 JSON 响应体设计的，
// 图片等二进制解码结果不像 JSON 会被退回 base64 原文，拼进 data URL 后形成双重编码，
// 上游直接以 "Could not process image" 拒绝，且会话历史每轮重放导致会话永久失败。
func binaryFromPayload(value interface{}) []byte {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return v
	case string:
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
			return decoded
		}
		return []byte(v)
	default:
		body, _ := json.Marshal(v)
		return body
	}
}

func looksLikeJSON(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func usageFromPayload(value interface{}) *sdk.Usage {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var usage sdk.Usage
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil
	}
	return &usage
}

func mapFromAny(value interface{}) (map[string]interface{}, bool) {
	if value == nil {
		return nil, false
	}
	if m, ok := value.(map[string]interface{}); ok {
		return m, true
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false
	}
	return out, true
}

func stringFromAny(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func intFromAny(value interface{}) int {
	return int(int64FromAny(value))
}

func int64FromAny(value interface{}) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	default:
		return 0
	}
}
