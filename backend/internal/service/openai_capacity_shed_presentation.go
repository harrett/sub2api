package service

// openai_capacity_shed_presentation.go — fork 本地文件（upstream 不存在）。
//
// OpenAI 原生流式转发（/v1/responses）中途遇到上游容量降载
// （isOpenAIUpstreamCapacityShedEvent，即 "server is overloaded" /
// "servers are currently overloaded" 之类的信号）时，如果客户端已经开始收到
// 输出 token（clientOutputStarted=true），网关无法再切账号重试 —— 已经发出去
// 的内容没法收回。这种情况下只能把上游的失败事件原样转发，此前上游原始
// message（例如 "Our servers are currently overloaded. Please try again
// later."）会一字不改地到达客户端，用户完全无法判断这是不是自己的账户问题。
//
// upstreamSanitizeOpenAIResponseFailedEventForClient 已经在做类似的事，但只
// 改写 error/response.error 的 code 字段（server_is_overloaded/slow_down →
// server_error，让 Codex CLI 从"直接终止会话"改为走它自己的重试路径），故意
// 不碰 message —— 那是 upstream 自己的责任边界，改错误码是为了客户端重试语义
// 正确，不涉及"这段话说给谁听"的问题。
//
// 本文件只做一件新事：命中容量降载时，在 message 字段追加一次责任方标签，
// 复用 internal/pkg/gatewayerr 里与另外四条链路共用的同一张指引表，让用户看
// 到的措辞与 API Key 鉴权中间件、计费检查、账号调度、并发槽位四条链路统一。
//
// 只改 message，不碰 upstream 已经在维护的 code/type 改写逻辑，也不改变
// sanitized 判定之外的任何转发路径 —— failover 可行时（clientOutputStarted=
// false 且判定为可重试）请求早在到达这里之前就已经切账号重试，根本不会走到
// 这个函数；只有 failover 不可行、必须原样转发的场景才会经过这里。
//
// 包一层而不是改 upstream 函数体，是为了让 upstream 对该函数内部改写逻辑的
// 后续变更仍能干净合入 —— 那边只有函数名一行属于本 fork。

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Wei-Shaw/sub2api/internal/pkg/gatewayerr"
)

// upstreamCapacityShedMidstreamGuidanceCode 对应 gatewayerr 指引表里的
// UPSTREAM_CAPACITY_SHED_MIDSTREAM 条目。
const upstreamCapacityShedMidstreamGuidanceCode = "UPSTREAM_CAPACITY_SHED_MIDSTREAM"

// sanitizeOpenAIResponseFailedEventForClient 是全部调用点使用的入口，
// 签名与 upstream 一致。
func sanitizeOpenAIResponseFailedEventForClient(payload []byte, eventType string, clientOutputStarted bool) ([]byte, bool) {
	updated, changed := upstreamSanitizeOpenAIResponseFailedEventForClient(payload, eventType, clientOutputStarted)

	labeled, labelChanged := labelOpenAICapacityShedMessage(updated, eventType)
	if !labelChanged {
		return updated, changed
	}
	return labeled, true
}

// labelOpenAICapacityShedMessage 只在"确认是容量降载事件"时改写 message 字段，
// 对其它一切错误原样透传 —— 与另外四条链路一样，未登记的错误码不瞎贴标签。
func labelOpenAICapacityShedMessage(payload []byte, eventType string) ([]byte, bool) {
	eventType = strings.TrimSpace(eventType)
	if eventType != "response.failed" && eventType != "error" {
		return payload, false
	}
	if len(payload) == 0 || !gjson.ValidBytes(payload) || !isOpenAIUpstreamCapacityShedEvent(payload) {
		return payload, false
	}

	errorPath := ""
	switch {
	case gjson.GetBytes(payload, "response.error").Exists():
		errorPath = "response.error"
	case gjson.GetBytes(payload, "error").Exists():
		errorPath = "error"
	default:
		return payload, false
	}

	original := extractOpenAISSEErrorMessage(payload)
	_, message := gatewayerr.PlatformErrorPresentation(upstreamCapacityShedMidstreamGuidanceCode, 0, original)

	next, err := sjson.SetBytes(payload, errorPath+".message", message)
	if err != nil {
		return payload, false
	}
	return next, true
}
