package playground

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 回归（生产事故 2026-07-10）：会话历史里的 /assets-runtime/ 图片引用，经
// rewriteChatImageAssetURLs 展开 + compileChatForwardPlan 编译后，最终发往上游的
// 图块字节必须与磁盘原图一致。修复前 Host.Invoke 返回的 base64 字符串被当作原始
// 字节二次编码，任何"离开会话再回来"的图片历史都会让后续全部请求被上游 400 拒绝，
// 会话永久无法恢复。
func TestRewriteAssetURLThenCompileClaudeKeepsImageBytes(t *testing.T) {
	t.Parallel()

	original := testPNGBytes(t)
	host := &assetBytesHost{data: original, contentType: "image/png"}
	p := &Plugin{svc: &Service{storage: NewObjectStorage(host)}}

	body := []byte(`{"model":"claude-opus-4-8","stream":true,"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"看看这张图"},` +
		`{"type":"image_url","image_url":{"url":"/assets-runtime/chat/36/202607/demo.png"}}]}]}`)

	rewritten, err := p.rewriteChatImageAssetURLs(context.Background(), body)
	if err != nil {
		t.Fatalf("rewriteChatImageAssetURLs() error = %v", err)
	}
	if host.gotObjectKey != "chat/36/202607/demo.png" {
		t.Fatalf("object_key = %q", host.gotObjectKey)
	}

	// 展开后的 image_url 必须是可解回原始字节的 data URL
	var req struct {
		Messages []struct {
			Content []struct {
				Type     string `json:"type"`
				ImageURL *struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rewritten, &req); err != nil {
		t.Fatalf("unmarshal rewritten body: %v", err)
	}
	var dataURL string
	for _, part := range req.Messages[0].Content {
		if part.Type == "image_url" && part.ImageURL != nil {
			dataURL = part.ImageURL.URL
		}
	}
	mediaType, b64, ok := parseImageDataURL(dataURL)
	if !ok {
		t.Fatalf("展开结果不是合法图片 data URL: %.60s...", dataURL)
	}
	if mediaType != "image/png" {
		t.Fatalf("media type = %q", mediaType)
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("data URL base64 解码失败: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Fatalf("data URL 载荷与原图不一致（len=%d want %d）——出现双重 base64 编码", len(decoded), len(original))
	}

	// 继续走 Claude 编译：最终 image block 的 source.data 也必须解回原图
	plan, err := compileChatForwardPlan("claude", rewritten)
	if err != nil {
		t.Fatalf("compileChatForwardPlan() error = %v", err)
	}
	var claudeReq struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(plan.Body, &claudeReq); err != nil {
		t.Fatalf("unmarshal claude body: %v", err)
	}
	var imageData, imageMedia string
	for _, block := range claudeReq.Messages[0].Content {
		if block["type"] == "image" {
			source, _ := block["source"].(map[string]any)
			imageData, _ = source["data"].(string)
			imageMedia, _ = source["media_type"].(string)
		}
	}
	if imageData == "" {
		t.Fatal("claude 请求缺少 image block（图片被静默丢弃）")
	}
	if imageMedia != "image/png" {
		t.Fatalf("claude image media_type = %q", imageMedia)
	}
	claudeBytes, err := base64.StdEncoding.DecodeString(imageData)
	if err != nil {
		t.Fatalf("claude image data 解码失败: %v", err)
	}
	if !bytes.Equal(claudeBytes, original) {
		t.Fatalf("claude image 字节与原图不一致（len=%d want %d）", len(claudeBytes), len(original))
	}
}

func TestValidateChatForwardBodySize(t *testing.T) {
	t.Parallel()

	if err := validateChatForwardBodySize(maxChatForwardBodyBytes); err != nil {
		t.Fatalf("body at limit should pass, got %v", err)
	}
	err := validateChatForwardBodySize(maxChatForwardBodyBytes + 1)
	if err == nil {
		t.Fatal("body above limit should be rejected")
	}
	if !strings.Contains(err.Error(), "30MB") {
		t.Fatalf("error = %q, want actionable size hint", err.Error())
	}
}

func TestWriteHostForwardErrorInvalidArgument(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()

	writeHostForwardError(recorder, status.Error(codes.InvalidArgument, "图片过大，请压缩后重试"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "图片过大，请压缩后重试") {
		t.Fatalf("body = %q, want contain image too large message", body)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "invalid_request_error") {
		t.Fatalf("body = %q, want invalid_request_error", body)
	}
}

func TestWriteHostForwardErrorUnavailable(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()

	writeHostForwardError(recorder, status.Error(codes.Unavailable, "upstream exploded"))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "upstream exploded") {
		t.Fatalf("body = %q, want sanitized upstream error", body)
	}
	if !strings.Contains(body, "The request could not be completed. Please try again later.") {
		t.Fatalf("body = %q, want generic retry message", body)
	}
}

func TestModelSupportsChat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		caps []string
		want bool
	}{
		{"empty defaults to chat", nil, true},
		{"explicit chat", []string{"chat", "reasoning"}, true},
		{"image only excluded", []string{"image_generation"}, false},
		{"case insensitive", []string{"Chat"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelSupportsChat(tc.caps); got != tc.want {
				t.Fatalf("modelSupportsChat(%v) = %v, want %v", tc.caps, got, tc.want)
			}
		})
	}
}
