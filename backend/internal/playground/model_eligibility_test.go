package playground

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// eligibleGroupsHost 模拟 core 的 groups.list(eligible_only)：按平台返回分组数，
// 未登记的平台返回错误（模拟 core 查询失败）。
type eligibleGroupsHost struct {
	groupsByPlatform map[string]int
	calls            []string
}

func (h *eligibleGroupsHost) Invoke(_ context.Context, req sdk.HostInvokeRequest) (*sdk.HostInvokeResponse, error) {
	if req.Method != hostMethodGroupsList {
		return nil, fmt.Errorf("unexpected method %s", req.Method)
	}
	if v, _ := req.Payload["eligible_only"].(bool); !v {
		return nil, errors.New("eligible_only 未置真")
	}
	if id, _ := req.Payload["user_id"].(int64); id <= 0 {
		return nil, errors.New("user_id 缺失")
	}
	platform, _ := req.Payload["platform"].(string)
	h.calls = append(h.calls, platform)
	count, ok := h.groupsByPlatform[platform]
	if !ok {
		return nil, status.Error(codes.Internal, "boom")
	}
	groups := make([]interface{}, 0, count)
	for i := 0; i < count; i++ {
		groups = append(groups, map[string]interface{}{"id": float64(i + 1), "platform": platform})
	}
	return &sdk.HostInvokeResponse{Status: "ok", Payload: map[string]interface{}{"groups": groups}}, nil
}

func (h *eligibleGroupsHost) InvokeStream(context.Context, sdk.HostStreamRequest) (sdk.HostStream, error) {
	return nil, errors.New("stream not supported in eligibleGroupsHost")
}

func TestFilterChatModelsByEligibility(t *testing.T) {
	t.Parallel()

	models := []hostModelInfo{
		{ID: "claude-opus-4-8", Platform: "claude"},
		{ID: "gpt-5.5", Platform: "openai"},
		{ID: "gemini-3.5-flash", Platform: "gemini"},
	}
	ids := func(items []hostModelInfo) []string {
		out := make([]string, 0, len(items))
		for _, m := range items {
			out = append(out, m.ID)
		}
		return out
	}
	tests := []struct {
		name     string
		eligible platformEligibility
		want     []string
	}{
		{name: "空表＝全部放行（旧行为）", eligible: platformEligibility{}, want: []string{"claude-opus-4-8", "gpt-5.5", "gemini-3.5-flash"}},
		{name: "nil 表同样放行", eligible: nil, want: []string{"claude-opus-4-8", "gpt-5.5", "gemini-3.5-flash"}},
		{name: "白名单排除 claude", eligible: platformEligibility{"claude": false, "openai": true, "gemini": true}, want: []string{"gpt-5.5", "gemini-3.5-flash"}},
		{name: "查询失败的平台视为未知放行", eligible: platformEligibility{"claude": false}, want: []string{"gpt-5.5", "gemini-3.5-flash"}},
		{name: "全部无资格→空列表", eligible: platformEligibility{"claude": false, "openai": false, "gemini": false}, want: []string{}},
		{name: "平台大小写不敏感", eligible: platformEligibility{"openai": false}, want: []string{"claude-opus-4-8", "gemini-3.5-flash"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ids(filterChatModelsByEligibility(models, tt.eligible))
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("filterChatModelsByEligibility() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlatformEligibilityAllowedPlatforms(t *testing.T) {
	t.Parallel()

	eligible := platformEligibility{"claude": false, "openai": true}
	got := eligible.allowedPlatforms([]string{"claude", "openai", "gemini"})
	if fmt.Sprint(got) != fmt.Sprint([]string{"openai", "gemini"}) {
		t.Fatalf("allowedPlatforms() = %v", got)
	}
}

func TestResolvePlatformEligibility(t *testing.T) {
	t.Parallel()

	t.Run("按 core 结果判定，查询失败的平台不入表", func(t *testing.T) {
		t.Parallel()
		host := &eligibleGroupsHost{groupsByPlatform: map[string]int{"claude": 0, "openai": 2}}
		p := &Plugin{host: host}
		got := p.resolvePlatformEligibility(context.Background(), 42, []string{"claude", "openai", "gemini"})
		if allowed, known := got["claude"]; !known || allowed {
			t.Fatalf("claude eligibility = (%v,%v), want (false,true)", allowed, known)
		}
		if allowed, known := got["openai"]; !known || !allowed {
			t.Fatalf("openai eligibility = (%v,%v), want (true,true)", allowed, known)
		}
		if _, known := got["gemini"]; known {
			t.Fatal("gemini 查询失败应视为未知，不应入表")
		}
		if len(host.calls) != 3 {
			t.Fatalf("groups.list calls = %v", host.calls)
		}
	})

	t.Run("无用户身份或 host 不可用时全部放行", func(t *testing.T) {
		t.Parallel()
		host := &eligibleGroupsHost{groupsByPlatform: map[string]int{"claude": 0}}
		if got := (&Plugin{host: host}).resolvePlatformEligibility(context.Background(), 0, []string{"claude"}); len(got) != 0 {
			t.Fatalf("user_id=0 应返回空表，got %v", got)
		}
		if len(host.calls) != 0 {
			t.Fatal("user_id=0 不应调用 groups.list")
		}
		if got := (&Plugin{}).resolvePlatformEligibility(context.Background(), 42, []string{"claude"}); len(got) != 0 {
			t.Fatalf("host=nil 应返回空表，got %v", got)
		}
	})
}

func TestForwardErrorIsMemberGroupForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		groups   map[string]int
		platform string
		want     bool
	}{
		{name: "core 显式拒绝（PermissionDenied+白名单文案）", err: status.Error(codes.PermissionDenied, "所属团队成员无权使用该分组"), groups: map[string]int{"claude": 3}, platform: "claude", want: true},
		{name: "其他 PermissionDenied 不误判", err: status.Error(codes.PermissionDenied, "成员已停用"), groups: map[string]int{"claude": 0}, platform: "claude", want: false},
		{name: "通用 Unavailable 且该平台无任何可用分组→权限问题", err: status.Error(codes.Unavailable, "请求暂时无法完成，请稍后重试"), groups: map[string]int{"claude": 0}, platform: "claude", want: true},
		{name: "通用 Unavailable 但仍有可用分组→上游故障", err: status.Error(codes.Unavailable, "请求暂时无法完成，请稍后重试"), groups: map[string]int{"claude": 1}, platform: "claude", want: false},
		{name: "资格查询失败时不定性为权限问题", err: status.Error(codes.Unavailable, "请求暂时无法完成，请稍后重试"), groups: map[string]int{}, platform: "claude", want: false},
		{name: "余额不足不归入权限问题", err: status.Error(codes.ResourceExhausted, "余额不足"), groups: map[string]int{"claude": 0}, platform: "claude", want: false},
		{name: "非 gRPC 错误不归入权限问题", err: errors.New("dial tcp: connection refused"), groups: map[string]int{"claude": 0}, platform: "claude", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Plugin{host: &eligibleGroupsHost{groupsByPlatform: tt.groups}}
			if got := p.forwardErrorIsMemberGroupForbidden(context.Background(), tt.err, 42, tt.platform); got != tt.want {
				t.Fatalf("forwardErrorIsMemberGroupForbidden() = %v, want %v", got, tt.want)
			}
		})
	}
}
