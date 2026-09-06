package convlog

import "strings"

// redactedPlaceholder 是脱敏后写入的占位符，保留字段存在性但不泄露值。
const redactedPlaceholder = "[REDACTED]"

// sensitiveJSONKeys 是落盘前必须抹掉的键名（大小写不敏感、精确匹配）。
//
// 只做精确键名匹配，不做子串匹配：`max_tokens`、`token_count` 这类合法字段不能被误伤，
// 而 `token` 单独作为键出现在聊天请求体里几乎必然是凭证。
var sensitiveJSONKeys = map[string]struct{}{
	"authorization":       {},
	"api_key":             {},
	"apikey":              {},
	"x-api-key":           {},
	"access_token":        {},
	"accesstoken":         {},
	"refresh_token":       {},
	"refreshtoken":        {},
	"id_token":            {},
	"session_token":       {},
	"client_secret":       {},
	"secret":              {},
	"secret_access_key":   {},
	"password":            {},
	"passwd":              {},
	"cookie":              {},
	"set-cookie":          {},
	"token":               {},
	"bearer":              {},
	"credentials":         {},
	"service_account_key": {},
	"private_key":         {},
}

// droppedJSONKeys 是整段删除（而非替换成占位符）的字段：内容不透明，
// 对训练和风控都零价值，留着只占体积。
//
// encrypted_content 是 OpenAI 给 reasoning 项附带的密文，实测单条 Codex 记录
// 里就有 64KB，占该记录的 5.4%，而人和模型都永远读不了它。
var droppedJSONKeys = map[string]struct{}{
	"encrypted_content": {},
	// 图片载荷：用户上传的参考图与模型生成的图都不留存，只留提示词文本。
	// 单张图 base64 就能到 MB 级，而风控要看的是"让模型做什么"，不是图本身。
	"b64_json":    {}, // OpenAI 生图响应
	"inline_data": {}, // Gemini 请求/响应内联图
	"inlineData":  {}, // Gemini 驼峰写法
}

// imageDataURIPrefix 标识内联图片的 data URI。这类值可能出现在任何字段名下
// （image_url.url、source.data、自定义字段），只能按值识别。
const imageDataURIPrefix = "data:image/"

// imagePlaceholder 替换掉图片载荷，保留"这里原本有张图"这个事实。
const imagePlaceholder = "[IMAGE]"

// redactJSON 递归清洗 JSON 值：凭证字段替换成占位符，不透明字段整段删除。
// 传入的是 encoding/json 解码结果（map[string]any / []any / 标量），
// 原地改写并返回同一个值。
//
// 深度上限防御畸形深嵌套导致的栈增长；超深部分整体丢弃而不是继续递归。
func redactJSON(value any) any {
	return redactJSONDepth(value, 0)
}

const maxRedactDepth = 64

func redactJSONDepth(value any, depth int) any {
	if depth > maxRedactDepth {
		return redactedPlaceholder
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if _, dropped := droppedJSONKeys[normalized]; dropped {
				delete(typed, key)
				continue
			}
			if _, sensitive := sensitiveJSONKeys[normalized]; sensitive {
				typed[key] = redactedPlaceholder
				continue
			}
			typed[key] = redactJSONDepth(child, depth+1)
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = redactJSONDepth(child, depth+1)
		}
		return typed
	case string:
		if strings.HasPrefix(typed, imageDataURIPrefix) {
			return imagePlaceholder
		}
		return typed
	default:
		return value
	}
}
