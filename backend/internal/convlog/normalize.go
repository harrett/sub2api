package convlog

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// DetectProtocol 依据入站路径与请求体形态判断协议。
// 路径优先：同一个 body 形态在不同端点上语义不同（例如 /v1/responses 与 /v1/messages）。
func DetectProtocol(endpoint string, body []byte) string {
	path := strings.ToLower(endpoint)
	switch {
	case strings.Contains(path, "/responses"):
		return ProtocolOpenAIResponses
	case strings.Contains(path, "/chat/completions"):
		return ProtocolOpenAIChat
	case strings.Contains(path, "/messages"):
		return ProtocolAnthropicMessages
	case strings.Contains(path, "generatecontent"), strings.Contains(path, "/v1beta/"):
		return ProtocolGeminiGenerate
	case strings.Contains(path, "/images/"):
		return ProtocolOpenAIImages
	}
	if !gjson.ValidBytes(body) {
		return ProtocolUnknown
	}
	switch {
	case gjson.GetBytes(body, "contents").IsArray():
		return ProtocolGeminiGenerate
	case gjson.GetBytes(body, "input").Exists():
		return ProtocolOpenAIResponses
	case gjson.GetBytes(body, "system").Exists():
		return ProtocolAnthropicMessages
	case gjson.GetBytes(body, "messages").IsArray():
		return ProtocolOpenAIChat
	}
	return ProtocolUnknown
}

// ExtractPreview 返回用于风控检索的用户输入预览，按 UTF-8 边界截断到 limit 字节。
//
// 不能复用内容审核的提取器：那套逻辑只看 messages/input 的**最后一个元素**，
// 因为审核关心"用户刚发出的这一句"。风控关心的是"用户到底打了什么字"，而
// agent 流量（Codex/Claude Code）的最后一个元素通常是 function_call_output 或
// tool_result，按审核语义取会得到空串——这正是列表里出现"（无文本输入）"而
// 全文里明明有用户输入的原因。这里改成从后往前找最近一条**真正的用户文本**。
func ExtractPreview(protocol string, body []byte, limit int) string {
	// 预览是列表里的一行，压成单行；LastUserText 本身保留原始换行供训练使用。
	return truncateUTF8(strings.Join(strings.Fields(LastUserText(protocol, body)), " "), limit)
}

// LastUserText 返回本轮最后一条真实用户输入的原文（保留换行，不截断）。
//
// "最后一条"不是随便挑的：客户端注入的内容（系统提示、压缩历史、运行时快照、
// 技能目录）总是排在真人那句之前，两份生产抽样都如此。取最后一条既能避开这些
// 注入，又天然与列表预览一致——预览显示什么，全文里就是什么。
//
// 更早的那些真实用户输入不会丢：它们各自曾是所属那一轮请求的"最后一条"，
// 已由那一轮的记录保存过。存全量只会把同一句话在整个会话里抄 N 遍。
func LastUserText(protocol string, body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	return lastUserText(protocol, body)
}

func lastUserText(protocol string, body []byte) string {
	switch protocol {
	case ProtocolAnthropicMessages, ProtocolOpenAIChat:
		return lastUserTextFromMessages(gjson.GetBytes(body, "messages"))
	case ProtocolOpenAIResponses:
		return lastUserTextFromResponsesInput(gjson.GetBytes(body, "input"))
	case ProtocolGeminiGenerate:
		return lastUserTextFromGeminiContents(gjson.GetBytes(body, "contents"))
	case ProtocolOpenAIImages:
		// 生图端点没有对话数组，用户输入就是 prompt。漏掉它等于让风控对生图
		// 完全失明——而生图恰恰是常见的滥用面。
		return sanitizeUserText(gjson.GetBytes(body, "prompt").String())
	default:
		// 协议识别失败时挨个试，任何一个能取到就用它。
		for _, candidate := range []string{
			lastUserTextFromResponsesInput(gjson.GetBytes(body, "input")),
			lastUserTextFromMessages(gjson.GetBytes(body, "messages")),
			lastUserTextFromGeminiContents(gjson.GetBytes(body, "contents")),
		} {
			if candidate != "" {
				return candidate
			}
		}
		return ""
	}
}

func lastUserTextFromMessages(messages gjson.Result) string {
	return scanBackwards(messages, func(item gjson.Result) string {
		if !isUserRole(item.Get("role").String()) {
			return ""
		}
		return userTextFromContent(item.Get("content"))
	})
}

func lastUserTextFromResponsesInput(input gjson.Result) string {
	if input.Type == gjson.String {
		return sanitizeUserText(input.String())
	}
	return scanBackwards(input, func(item gjson.Result) string {
		if !isUserRole(item.Get("role").String()) {
			return ""
		}
		if text := userTextFromContent(item.Get("content")); text != "" {
			return text
		}
		// Responses 的 input 项也可能直接是 {"type":"input_text","text":...}
		return sanitizeUserText(item.Get("text").String())
	})
}

func lastUserTextFromGeminiContents(contents gjson.Result) string {
	return scanBackwards(contents, func(item gjson.Result) string {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role != "" && role != "user" {
			return ""
		}
		return sanitizeUserText(flattenGeminiParts(item.Get("parts")))
	})
}

// scanBackwards 从数组末尾往前找第一个能提取出文本的元素。
func scanBackwards(array gjson.Result, extract func(gjson.Result) string) string {
	if !array.IsArray() {
		return ""
	}
	items := array.Array()
	for i := len(items) - 1; i >= 0; i-- {
		if text := extract(items[i]); text != "" {
			return text
		}
	}
	return ""
}

// isUserRole 把 Responses 的 message 项与 chat 的 user 消息统一看待。
func isUserRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "user")
}

// userTextFromContent 从 content（字符串或块数组）里取出纯文本，
// 忽略 tool_result / image 这类非用户输入的块。
func userTextFromContent(content gjson.Result) string {
	switch {
	case !content.Exists():
		return ""
	case content.Type == gjson.String:
		return sanitizeUserText(content.String())
	case content.IsArray():
		var parts []string
		content.ForEach(func(_, block gjson.Result) bool {
			switch strings.ToLower(strings.TrimSpace(block.Get("type").String())) {
			case "", "text", "input_text":
				if text := sanitizeUserText(block.Get("text").String()); text != "" {
					parts = append(parts, text)
				}
			}
			return true
		})
		return strings.Join(parts, "\n")
	case content.IsObject():
		return sanitizeUserText(content.Get("text").String())
	default:
		return ""
	}
}

// injectedBlockTags 是客户端**追加在用户消息内部**的成对标签。这类注入不能整条丢弃：
// 抽样 3 里用户真正打的是"进行修复"，客户端在同一条消息后面接了一整块
// <environment_details>（含每轮都变的时间戳）。整条丢会丢掉用户输入，整条留会把
// 时间戳噪音写进语料，所以必须只挖掉标签块、留下人写的部分。
var injectedBlockTags = []string{
	"system-reminder",        // Claude Code
	"environment_details",    // Cline / Roo / KFlash 系客户端
	"workspace_attachment",   // AIDE 系客户端：附着工作区的文件树与变更清单
	"codex_internal_context", // Codex 的目标续跑指令，整块都是机器生成的
}

// injectedBlockPatterns 匹配成对标签及其内容；dangling 匹配没有闭合标签的残缺块
// （被上游截断时会出现），从开标签处一路截掉，避免注入内容泄进语料。
var (
	injectedBlockPatterns = compileInjectedBlockPatterns()
	danglingBlockPatterns = compileDanglingBlockPatterns()
)

// 连同标签两侧的水平空白一起吃掉，避免挖走块之后在行内留下双空格。
// 只吃空格与制表符，不碰换行——用户输入里的代码缩进必须原样保留。
func compileInjectedBlockPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(injectedBlockTags))
	for _, tag := range injectedBlockTags {
		out = append(out, regexp.MustCompile(`(?is)[ \t]*<`+tag+`\b[^>]*>.*?</`+tag+`\s*>[ \t]*`))
	}
	return out
}

func compileDanglingBlockPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(injectedBlockTags))
	for _, tag := range injectedBlockTags {
		out = append(out, regexp.MustCompile(`(?is)[ \t]*<`+tag+`\b[^>]*>.*\z`))
	}
	return out
}

var (
	blankLinePattern       = regexp.MustCompile(`(?m)^[ \t]+$`)
	repeatedNewlinePattern = regexp.MustCompile(`\n{3,}`)
)

// injectedContentMarkers 是**整条消息都是注入内容**时的特征串。与上面的成对标签不同，
// 这些注入不带包裹标签，只能整条识别；某些客户端（DeepSeek Harness）97% 的 user
// 字节都是这类东西。把它们当成用户输入会让风控看错人，也会让蒸馏语料被样板淹没。
var injectedContentMarkers = []string{
	// 会话压缩检查点：客户端自动生成的历史摘要
	"This is an automatically generated checkpoint condensing an earlier span of the conversation",
	// 运行时上下文快照，每轮重发
	"Current runtime context. This snapshot supersedes earlier runtime-context snapshots",
}

// stripInjectedBlocks 挖掉用户消息内部的注入块，保留人写的部分。
//
// 块被替换成单个空格而不是直接删除：行内注入（"before <env>…</env> after"）
// 删干净会把两个词粘在一起。之后再收拾挖块留下的空白行。
func stripInjectedBlocks(text string) string {
	for _, pattern := range injectedBlockPatterns {
		text = pattern.ReplaceAllString(text, " ")
	}
	for _, pattern := range danglingBlockPatterns {
		text = pattern.ReplaceAllString(text, " ")
	}
	text = blankLinePattern.ReplaceAllString(text, "")
	return repeatedNewlinePattern.ReplaceAllString(text, "\n\n")
}

// sanitizeUserText 丢掉平台自己注入的上下文块，只留真正的用户输入，
// 并保留原始换行——训练要的是用户实际打出来的样子。
func sanitizeUserText(text string) string {
	text = strings.TrimSpace(stripInjectedBlocks(text))
	if text == "" {
		return ""
	}
	for _, marker := range injectedContentMarkers {
		if strings.Contains(text, marker) {
			return ""
		}
	}
	if service.IsInjectedPlatformPrompt(text) {
		return ""
	}
	return text
}

// responsesRoleFromType 推断 Responses 里不带 role 的项属于谁。
//
// Codex 的 agent 循环中这类项占绝大多数（reasoning / *_call / *_output），
// 早先它们统一落到 "user" 兜底上，于是一条只有 2 句真实用户输入的会话被标成
// 65 条 user——拿去蒸馏会让模型学到"工具输出是用户说的话"。
// 后缀判断能覆盖将来新增的 *_call / *_output 类型，认不出就明说 unknown。
func responsesRoleFromType(itemType string) string {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "reasoning", "agent_message":
		return RoleAssistant
	case "":
		return RoleUnknown
	}
	switch {
	case strings.HasSuffix(itemType, "_output"):
		return RoleTool
	case strings.HasSuffix(itemType, "_call"):
		return RoleAssistant
	default:
		return RoleUnknown
	}
}

// normalizeRoleName 把各协议的角色名收敛到统一取值。空值返回 unknown 而不是
// 猜一个——猜错的角色比缺失的角色危害大得多。
func normalizeRoleName(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user", "human":
		return RoleUser
	case "assistant", "model":
		return RoleAssistant
	case "tool", "function":
		return RoleTool
	case "":
		return RoleUnknown
	default:
		// system / developer 等原样保留，它们本身就是明确语义。
		return strings.ToLower(strings.TrimSpace(role))
	}
}

// flattenTextValue 把 string 或 [{type:text,text:...}] 结构压成纯文本。
func flattenTextValue(node gjson.Result) string {
	switch {
	case !node.Exists():
		return ""
	case node.Type == gjson.String:
		return node.String()
	case node.IsArray():
		var parts []string
		node.ForEach(func(_, item gjson.Result) bool {
			if text := item.Get("text").String(); text != "" {
				parts = append(parts, text)
			}
			return true
		})
		return strings.Join(parts, "\n")
	case node.IsObject():
		return node.Get("text").String()
	default:
		return ""
	}
}

func flattenGeminiParts(node gjson.Result) string {
	if !node.IsArray() {
		return node.Get("text").String()
	}
	var parts []string
	node.ForEach(func(_, item gjson.Result) bool {
		if text := item.Get("text").String(); text != "" {
			parts = append(parts, text)
		}
		return true
	})
	return strings.Join(parts, "\n")
}

// decodeJSON 把原始 JSON 片段解成 any，失败时返回原始字符串而不是丢弃。
func decodeJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return raw
	}
	return out
}

func joinNonEmpty(existing, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if existing == "" {
		return addition
	}
	return existing + "\n" + addition
}

// truncateUTF8 按 UTF-8 边界截断，避免在多字节字符中间切断产生乱码。
func truncateUTF8(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut]
}

// utf8Start 判断字节是否为 UTF-8 序列的首字节（非 10xxxxxx 续字节）。
func utf8Start(b byte) bool {
	return b&0xC0 != 0x80
}
