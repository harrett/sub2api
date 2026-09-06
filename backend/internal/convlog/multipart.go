package convlog

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// multipartTextFieldLimit 单个文本字段的读取上限。表单里的 prompt 不会很长，
// 给足余量即可；超过就截断，绝不因为一个畸形字段把内存吃掉。
const multipartTextFieldLimit = 64 << 10

// multipartPromptFields 是各生图端点承载用户提示词的表单字段名。
var multipartPromptFields = []string{"prompt", "input", "text"}

// userInputFor 取本轮的用户输入。生图的 multipart 表单走独立分支：
// 它不是 JSON，gjson 解析不出任何东西。
func userInputFor(protocol string, input CaptureInput) string {
	if isMultipartContentType(input.ContentType) {
		return multipartPrompt(input.RequestBody, input.ContentType)
	}
	return LastUserText(protocol, input.RequestBody)
}

func isMultipartContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/form-data")
}

// multipartPrompt 从 multipart 表单里只取出提示词文本。
//
// 参考图片（image/mask 等文件分块）一律跳过：用户上传的参考图不留存，风控要看的
// 是他让模型做什么。这样 /v1/images/edits 既能被追溯，又不会把图片写进对象存储。
func multipartPrompt(body []byte, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	boundary := params["boundary"]
	if boundary == "" {
		return ""
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	found := make(map[string]string)
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		// 有文件名的分块就是上传的文件（参考图/蒙版），直接跳过，不读内容。
		if part.FileName() != "" {
			_ = part.Close()
			continue
		}
		name := strings.ToLower(strings.TrimSpace(part.FormName()))
		if !isPromptField(name) {
			_ = part.Close()
			continue
		}
		value, readErr := io.ReadAll(io.LimitReader(part, multipartTextFieldLimit))
		_ = part.Close()
		if readErr != nil && len(value) == 0 {
			continue
		}
		if text := sanitizeUserText(string(value)); text != "" {
			found[name] = text
		}
	}

	for _, field := range multipartPromptFields {
		if text, ok := found[field]; ok {
			return text
		}
	}
	return ""
}

func isPromptField(name string) bool {
	for _, field := range multipartPromptFields {
		if name == field {
			return true
		}
	}
	return false
}
