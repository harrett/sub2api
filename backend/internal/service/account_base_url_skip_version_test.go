package service

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamBaseURLSkipVersion(t *testing.T) {
	tests := []struct {
		name  string
		creds map[string]any
		want  bool
	}{
		{name: "missing key defaults to false", creds: map[string]any{"base_url": "https://image.aigw.store/api-proxy"}, want: false},
		{name: "nil credentials", creds: nil, want: false},
		{name: "bool true", creds: map[string]any{"base_url_skip_version": true}, want: true},
		{name: "bool false", creds: map[string]any{"base_url_skip_version": false}, want: false},
		{name: "string true", creds: map[string]any{"base_url_skip_version": "true"}, want: true},
		{name: "string false", creds: map[string]any{"base_url_skip_version": "false"}, want: false},
		{name: "json number one", creds: map[string]any{"base_url_skip_version": json.Number("1")}, want: true},
		{name: "json number zero", creds: map[string]any{"base_url_skip_version": json.Number("0")}, want: false},
		{name: "unparsable stays false", creds: map[string]any{"base_url_skip_version": []string{"yes"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: tt.creds}
			require.Equal(t, tt.want, account.UpstreamBaseURLSkipVersion())
		})
	}
}

func TestUpstreamBaseURLSkipVersionNilAccount(t *testing.T) {
	var account *Account
	require.False(t, account.UpstreamBaseURLSkipVersion())
}

// TestBuildOpenAIImagesURLWithSkipVersionAccount 覆盖 image.aigw.store 形状的上游：
// 图像端点挂在 /api-proxy 下且路径不含 /v1，generations 与 edits 都必须命中。
func TestBuildOpenAIImagesURLWithSkipVersionAccount(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":              "https://image.aigw.store/api-proxy",
			"base_url_skip_version": true,
		},
	}
	base := account.GetOpenAIBaseURL()
	require.Equal(t, "https://image.aigw.store/api-proxy", base)

	require.Equal(t,
		"https://image.aigw.store/api-proxy/images/generations",
		buildOpenAIImagesURL(base, openAIImagesGenerationsEndpoint, account.UpstreamBaseURLSkipVersion()),
	)
	require.Equal(t,
		"https://image.aigw.store/api-proxy/images/edits",
		buildOpenAIImagesURL(base, openAIImagesEditsEndpoint, account.UpstreamBaseURLSkipVersion()),
	)

	// 同一个 base 不开开关时保持既有（对该上游是 404 的）拼接，证明开关是唯一变量。
	require.Equal(t,
		"https://image.aigw.store/api-proxy/v1/images/generations",
		buildOpenAIImagesURL(base, openAIImagesGenerationsEndpoint, false),
	)
}

func TestUpstreamModelListEndpointUnsupported(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{name: "not found", statusCode: http.StatusNotFound, want: true},
		{name: "method not allowed", statusCode: http.StatusMethodNotAllowed, want: true},
		// 只开放特定路径的上游（如只挂 /api-proxy/images/*）在网关层直接拒绝其余
		// 路径，同样等价于"没有模型列表端点"，管理后台同步应回退而非硬报错。
		{name: "forbidden", statusCode: http.StatusForbidden, want: true},
		// 401 是凭据失效的主信号，必须保持硬失败，否则坏 key 会被伪装成同步成功。
		{name: "unauthorized stays a real failure", statusCode: http.StatusUnauthorized, want: false},
		{name: "bad gateway is a real failure", statusCode: http.StatusBadGateway, want: false},
		{name: "internal error is a real failure", statusCode: http.StatusInternalServerError, want: false},
		{name: "no status code", statusCode: 0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &UpstreamModelSyncError{
				Kind:       UpstreamModelSyncErrorUpstream,
				Message:    "upstream model list failed",
				StatusCode: tt.statusCode,
			}
			require.Equal(t, tt.want, upstreamModelListEndpointUnsupported(err))
		})
	}
}
