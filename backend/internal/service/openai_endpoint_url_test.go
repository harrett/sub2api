package service

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildOpenAIEndpointURLPreservesURLComponents(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		endpoint string
		want     string
	}{
		{name: "root", base: "https://upstream.example", endpoint: "/v1/models", want: "https://upstream.example/v1/models"},
		{name: "v1", base: "https://upstream.example/v1", endpoint: "/v1/responses", want: "https://upstream.example/v1/responses"},
		{name: "prefix", base: "https://upstream.example/openai", endpoint: "/v1/chat/completions", want: "https://upstream.example/openai/v1/chat/completions"},
		{name: "version", base: "https://upstream.example/openai/v2", endpoint: "/v1/embeddings", want: "https://upstream.example/openai/v2/embeddings"},
		{name: "query", base: "https://upstream.example/v1?redirect=/", endpoint: "/v1/sub2api/billing", want: "https://upstream.example/v1/sub2api/billing?redirect=/"},
		{name: "fragment is removed", base: "https://upstream.example/v1#stale", endpoint: "/v1/alpha/search", want: "https://upstream.example/v1/alpha/search"},
		{name: "ipv6", base: "http://[2001:db8::1]:8080/v1?tenant=a#stale", endpoint: "/v1/responses/input_tokens", want: "http://[2001:db8::1]:8080/v1/responses/input_tokens?tenant=a"},
		{name: "already complete", base: "https://upstream.example/v1/images/generations?tenant=a", endpoint: "/v1/images/generations", want: "https://upstream.example/v1/images/generations?tenant=a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildOpenAIEndpointURL(tt.base, tt.endpoint, false))
		})
	}
}

// TestBuildOpenAIEndpointURLSkipVersion 覆盖账号级 base_url_skip_version=true 的拼接：
// 上游把 OpenAI 兼容接口挂在非版本号前缀下且路径不含 /v1（如 image.aigw.store 的
// /api-proxy/images/generations），此时必须恒用去掉 /v1 的相对路径。
func TestBuildOpenAIEndpointURLSkipVersion(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		endpoint string
		want     string
	}{
		{name: "custom prefix generations", base: "https://image.aigw.store/api-proxy", endpoint: "/v1/images/generations", want: "https://image.aigw.store/api-proxy/images/generations"},
		{name: "custom prefix edits", base: "https://image.aigw.store/api-proxy", endpoint: "/v1/images/edits", want: "https://image.aigw.store/api-proxy/images/edits"},
		{name: "custom prefix models", base: "https://image.aigw.store/api-proxy", endpoint: "/v1/models", want: "https://image.aigw.store/api-proxy/models"},
		{name: "custom prefix chat completions", base: "https://upstream.example/openai", endpoint: "/v1/chat/completions", want: "https://upstream.example/openai/chat/completions"},
		{name: "trailing slash", base: "https://image.aigw.store/api-proxy/", endpoint: "/v1/images/generations", want: "https://image.aigw.store/api-proxy/images/generations"},
		{name: "bare host", base: "https://upstream.example", endpoint: "/v1/responses", want: "https://upstream.example/responses"},
		{name: "query preserved", base: "https://image.aigw.store/api-proxy?tenant=a", endpoint: "/v1/images/edits", want: "https://image.aigw.store/api-proxy/images/edits?tenant=a"},
		// 端点本身无 /v1 前缀（DeepSeek 原生 Responses）时开关是 no-op。
		{name: "versionless endpoint unaffected", base: "https://upstream.example/openai", endpoint: "/responses", want: "https://upstream.example/openai/responses"},
		// base 已经是完整端点时不重复追加，与 skipVersion=false 的行为一致。
		{name: "already complete", base: "https://image.aigw.store/api-proxy/images/generations", endpoint: "/v1/images/generations", want: "https://image.aigw.store/api-proxy/images/generations"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildOpenAIEndpointURL(tt.base, tt.endpoint, true))
		})
	}
}

// TestBuildOpenAIEndpointURLSkipVersionDefaultIsInert 锁定回归安全性：不开启开关时
// 拼接结果必须与开关引入前逐字节一致。
func TestBuildOpenAIEndpointURLSkipVersionDefaultIsInert(t *testing.T) {
	bases := []string{
		"https://api.openai.com",
		"https://upstream.example/v1",
		"https://upstream.example/openai",
		"https://open.bigmodel.cn/api/paas/v4",
		"https://image.aigw.store/api-proxy",
	}
	endpoints := []string{
		"/v1/responses",
		"/v1/chat/completions",
		"/v1/embeddings",
		"/v1/models",
		openAIImagesGenerationsEndpoint,
		openAIImagesEditsEndpoint,
	}
	legacy := func(base, endpoint string) string {
		normalized := strings.TrimSpace(base)
		endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
		relative := strings.TrimPrefix(endpoint, "/v1")
		parsed, err := url.Parse(normalized)
		if err != nil {
			return strings.TrimRight(normalized, "/") + endpoint
		}
		path := strings.TrimRight(parsed.Path, "/")
		if !strings.HasSuffix(path, endpoint) && !strings.HasSuffix(path, relative) {
			if openAIBaseURLHasVersionSuffix(path) {
				path += relative
			} else {
				path += endpoint
			}
		}
		parsed.Path = path
		parsed.RawPath = ""
		parsed.Fragment = ""
		return parsed.String()
	}

	for _, base := range bases {
		for _, endpoint := range endpoints {
			require.Equal(t, legacy(base, endpoint), buildOpenAIEndpointURL(base, endpoint, false),
				"base=%s endpoint=%s", base, endpoint)
		}
	}
}
