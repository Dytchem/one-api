package gemini

// dyt-114: Gemini Interactions API 的"出口侧"适配（网关 -> 上游）。
//
// 与 dyt-113 的关系：
//   - dyt-113 做的是"入口侧"（客户端 -> 网关），收 /v1beta/interactions
//   - 本文件做的是"出口侧"（网关 -> 上游），发 /v1beta/interactions
// 两者合起来才能构成闭环：客户端可以用 Interactions 协议打进来，
// 网关也能用 Interactions 协议打给上游（此前只能发旧 generateContent）。
//
// 上游协议依据：https://ai.google.dev/api/interactions-api
//   POST {base}/v1beta/interactions
//   { "model": "...", "input": "..." } 或
//   { "model": "...", "input": [{"type":"user_input","content":[{"type":"text","text":".."}]}, ...] }
//   响应：{ id, model, status, output: [{type:"text",text}], usage:{input_tokens,output_tokens} }

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/model"
)

// ---- 请求结构 ----

type interactionsContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type interactionsInput struct {
	Type    string                `json:"type"`
	Content []interactionsContent `json:"content,omitempty"`
}

type interactionsRequest struct {
	Model  string              `json:"model,omitempty"`
	Input  []interactionsInput `json:"input"`
	Stream bool                `json:"stream,omitempty"`
	// 生成参数
	GenerationConfig  *interactionsGenerationConfig  `json:"generation_config,omitempty"`
	SystemInstruction *interactionsSystemInstruction `json:"system_instruction,omitempty"`
}

type interactionsGenerationConfig struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	MaxOutputTokens  int      `json:"max_output_tokens,omitempty"`
	StopSequences    []string `json:"stop_sequences,omitempty"`
	ResponseMIMEType string   `json:"response_mime_type,omitempty"`
}

type interactionsSystemInstruction struct {
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

// ---- 响应结构 ----

type interactionsUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type interactionsOutput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// function_call
	Id        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type interactionsResponse struct {
	Id     string               `json:"id"`
	Model  string               `json:"model"`
	Status string               `json:"status"`
	Output []interactionsOutput `json:"output"`
	Usage  *interactionsUsage   `json:"usage,omitempty"`
	Error  *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// ConvertRequestToInteractions 把 OpenAI chat 请求转换为 Interactions 请求。
//
// 映射要点：
//   - messages[].role=system         -> system_instruction.parts
//   - user/assistant 文本            -> input[] 的 user_input / model_output
//   - content 数组里的图片           -> {type:"image", data/mime_type}
//   - assistant 的 tool_calls        -> model_output 里的 function_call
//   - role=tool 的 tool 结果         -> user_input 里的 function_response（文本化）
//   - temperature/top_p/max_tokens   -> generation_config
func ConvertRequestToInteractions(textRequest model.GeneralOpenAIRequest) *interactionsRequest {
	req := &interactionsRequest{
		Model: textRequest.Model,
		Input: make([]interactionsInput, 0, len(textRequest.Messages)),
	}

	// generation_config
	gc := &interactionsGenerationConfig{
		Temperature:     textRequest.Temperature,
		TopP:            textRequest.TopP,
		MaxOutputTokens: textRequest.MaxTokens,
	}
	if s, ok := textRequest.Stop.(string); ok && s != "" {
		gc.StopSequences = []string{s}
	} else if arr, ok := textRequest.Stop.([]any); ok {
		for _, v := range arr {
			if sv, ok := v.(string); ok && sv != "" {
				gc.StopSequences = append(gc.StopSequences, sv)
			}
		}
	}
	if textRequest.ResponseFormat != nil {
		switch textRequest.ResponseFormat.Type {
		case "json_object":
			gc.ResponseMIMEType = "application/json"
		case "text":
			gc.ResponseMIMEType = "text/plain"
		}
	}
	req.GenerationConfig = gc
	if textRequest.Stream {
		req.Stream = true
	}

	var sysParts []string
	for _, message := range textRequest.Messages {
		if message.Role == "system" {
			if s := messageText(message); s != "" {
				sysParts = append(sysParts, s)
			}
			continue
		}

		switch message.Role {
		case "user":
			req.Input = append(req.Input, interactionsInput{
				Type:    "user_input",
				Content: openAIContentToInteractions(message),
			})
		case "assistant":
			contents := openAIContentToInteractions(message)
			// assistant 的 tool_calls 也放进同一个 model_output
			for _, tc := range message.ToolCalls {
				contents = append(contents, interactionsContent{
					Type: "function_call",
					Text: tc.Function.Name,
					Data: stringifyArguments(tc.Function.Arguments),
				})
			}
			if len(contents) > 0 {
				req.Input = append(req.Input, interactionsInput{
					Type:    "model_output",
					Content: contents,
				})
			}
		case "tool":
			// OpenAI 的 role=tool 回执 -> Interactions 的 user_input 文本内容。
			// Interactions 规范里函数结果以 function_response 表达，但各实现差异较大，
			// 这里退化为文本以最大兼容（内容不丢失）。
			if s := messageText(message); s != "" {
				req.Input = append(req.Input, interactionsInput{
					Type:    "user_input",
					Content: []interactionsContent{{Type: "text", Text: s}},
				})
			}
		default:
			// 未知角色按 user 处理
			if s := messageText(message); s != "" {
				req.Input = append(req.Input, interactionsInput{
					Type:    "user_input",
					Content: []interactionsContent{{Type: "text", Text: s}},
				})
			}
		}
	}

	// 没有任何 input 时给一个空文本，避免上游报"input required"
	if len(req.Input) == 0 {
		req.Input = append(req.Input, interactionsInput{
			Type:    "user_input",
			Content: []interactionsContent{{Type: "text", Text: ""}},
		})
	}

	if len(sysParts) > 0 {
		si := &interactionsSystemInstruction{}
		for _, p := range sysParts {
			si.Parts = append(si.Parts, struct {
				Text string `json:"text"`
			}{Text: p})
		}
		req.SystemInstruction = si
	}
	return req
}

// openAIContentToInteractions 把 OpenAI 消息的 content 转成 Interactions 内容块数组。
func openAIContentToInteractions(message model.Message) []interactionsContent {
	// 纯字符串
	if s, ok := message.Content.(string); ok {
		if s == "" {
			return nil
		}
		return []interactionsContent{{Type: "text", Text: s}}
	}
	// 多模态数组
	arr, ok := message.Content.([]any)
	if !ok {
		if s := messageText(message); s != "" {
			return []interactionsContent{{Type: "text", Text: s}}
		}
		return nil
	}
	out := make([]interactionsContent, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "text":
			if t, ok := m["text"].(string); ok && t != "" {
				out = append(out, interactionsContent{Type: "text", Text: t})
			}
		case "image_url":
			iu, ok := m["image_url"].(map[string]any)
			if !ok {
				continue
			}
			url, _ := iu["url"].(string)
			if url == "" {
				continue
			}
			// data URL -> data + mime_type；普通 URL -> uri
			if strings.HasPrefix(url, "data:") {
				if idx := strings.Index(url, ";base64,"); idx > 0 {
					mime := strings.TrimPrefix(url[:idx], "data:")
					data := url[idx+len(";base64,"):]
					out = append(out, interactionsContent{Type: "image", MimeType: mime, Data: data})
					continue
				}
			}
			out = append(out, interactionsContent{Type: "image", URI: url})
		}
	}
	return out
}

// messageText 提取消息的纯文本。
func messageText(message model.Message) string {
	if s, ok := message.Content.(string); ok {
		return s
	}
	arr, ok := message.Content.([]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := m["text"].(string); ok && t != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(t)
		}
	}
	return sb.String()
}

// stringifyArguments 把 tool_call 的 arguments（可能是 string 或对象）统一成 JSON 字符串。
func stringifyArguments(args any) string {
	switch v := args.(type) {
	case string:
		if v == "" {
			return "{}"
		}
		return v
	case nil:
		return "{}"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
}

// ---- 响应处理（出口侧）----

// InteractionsHandler 解析上游 Interactions 响应并转回 OpenAI chat 格式，
// 使网关对客户端保持统一的 OpenAI 输出（与既有 gemini Handler 行为一致）。
func InteractionsHandler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	_ = resp.Body.Close()

	var ir interactionsResponse
	if err := json.Unmarshal(responseBody, &ir); err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}
	// 上游错误体
	if ir.Error != nil && ir.Error.Message != "" {
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: ir.Error.Message,
				Type:    "upstream_error",
				Code:    resp.StatusCode,
			},
			StatusCode: resp.StatusCode,
		}, nil
	}

	// 汇总输出：文本拼接，function_call 转 tool_calls
	var textSB strings.Builder
	toolCalls := make([]model.Tool, 0)
	for _, o := range ir.Output {
		switch o.Type {
		case "text", "output_text":
			if o.Text != "" {
				textSB.WriteString(o.Text)
			}
		case "function_call":
			args := "{}"
			if len(o.Arguments) > 0 {
				args = string(o.Arguments)
			}
			id := o.Id
			if id == "" {
				id = "call_" + o.Name
			}
			toolCalls = append(toolCalls, model.Tool{
				Id:   id,
				Type: "function",
				Function: model.Function{
					Name:      o.Name,
					Arguments: args,
				},
			})
		default:
			// 未知类型若有 text 就取，尽量不丢内容
			if o.Text != "" {
				textSB.WriteString(o.Text)
			}
		}
	}
	text := textSB.String()

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	usage := model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: openai.CountTokenText(text, modelName),
	}
	if ir.Usage != nil {
		if ir.Usage.InputTokens > 0 {
			usage.PromptTokens = ir.Usage.InputTokens
		}
		if ir.Usage.OutputTokens > 0 {
			usage.CompletionTokens = ir.Usage.OutputTokens
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	openAIResponse := openai.TextResponse{
		Id:      ir.Id,
		Object:  "chat.completion",
		Created: helper.GetTimestamp(),
		Model:   modelName,
		Choices: []openai.TextResponseChoice{{
			Index: 0,
			Message: model.Message{
				Role:      "assistant",
				Content:   text,
				ToolCalls: toolCalls,
			},
			FinishReason: finishReason,
		}},
		Usage: usage,
	}
	if openAIResponse.Id == "" {
		openAIResponse.Id = "chatcmpl-" + modelName
	}

	jsonResponse, err := json.Marshal(openAIResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError), nil
	}
	return nil, &usage
}
