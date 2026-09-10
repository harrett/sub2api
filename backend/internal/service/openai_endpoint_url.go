package service

import (
	"net/url"
	"strings"
)

// buildOpenAIEndpointURL 把 OpenAI 协议端点拼到账号的上游 base URL 上。
//
// 默认按 base URL 路径的最后一段是否为版本段（v1 / v4 / v1beta …）推导：是则只
// 追加去掉 "/v1" 的相对路径，否则追加完整端点。
//
// skipVersion 是账号级开关（credentials.base_url_skip_version）：有些上游把
// OpenAI 兼容接口挂在非版本号前缀下且路径里没有 "/v1"（如
// https://host/api-proxy/images/generations），此时 base URL 无论怎么填都推导不
// 出"不要补 /v1"——只能由运维显式声明。开关为 true 时恒用相对路径，不再嗅探版本段。
func buildOpenAIEndpointURL(base string, endpoint string, skipVersion bool) string {
	normalized := strings.TrimSpace(base)
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	relative := strings.TrimPrefix(endpoint, "/v1")
	suffix := endpoint
	if skipVersion {
		suffix = relative
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return strings.TrimRight(normalized, "/") + suffix
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, endpoint) && !strings.HasSuffix(path, relative) {
		if skipVersion || openAIBaseURLHasVersionSuffix(path) {
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

func buildOpenAIResponsesInputTokensURL(base string, skipVersion bool) string {
	return buildOpenAIEndpointURL(base, "/v1/responses/input_tokens", skipVersion)
}

func openAIBaseURLHasVersionSuffix(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	pathValue := ""
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		pathValue = parsed.Path
	} else if slash := strings.Index(trimmed, "/"); slash >= 0 {
		pathValue = trimmed[slash:]
	}

	pathValue = strings.TrimRight(pathValue, "/")
	if pathValue == "" {
		return false
	}
	lastSlash := strings.LastIndex(pathValue, "/")
	segment := pathValue
	if lastSlash >= 0 {
		segment = pathValue[lastSlash+1:]
	}
	return isOpenAIAPIVersionSegment(segment)
}

func isOpenAIAPIVersionSegment(segment string) bool {
	s := strings.ToLower(strings.TrimSpace(segment))
	if len(s) < 2 || s[0] != 'v' || !isASCIIDigit(s[1]) {
		return false
	}

	i := 1
	for i < len(s) && isASCIIDigit(s[i]) {
		i++
	}
	if i == len(s) {
		return true
	}
	if s[i] == '.' {
		i++
		if i == len(s) || !isASCIIDigit(s[i]) {
			return false
		}
		for i < len(s) && isASCIIDigit(s[i]) {
			i++
		}
		return i == len(s)
	}

	suffix := s[i:]
	return strings.HasPrefix(suffix, "alpha") ||
		strings.HasPrefix(suffix, "beta") ||
		strings.HasPrefix(suffix, "preview")
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
