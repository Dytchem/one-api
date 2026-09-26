package controller

// dyt-113: Anthropic Messages API 原生入口（POST /v1/messages）。
//
// 背景：Claude Code / Anthropic SDK 等客户端只会说 Anthropic 协议。此前网关
// 只有 OpenAI 入口，这些客户端必须依赖第三方转换层。本文件实现"入口侧"
// 的 Anthropic -> OpenAI chat 转换，使原生 Anthropic 客户端可以直接打网关，
// 再由既有 adaptor 转发到任意通道（OpenAI 兼容 / Anthropic 上游均可）。
//
// 与 relay/adaptor/anthropic/adaptor.go 的区别：
//   - adaptor 是"出口侧"：OpenAI chat -> Anthropic 上游（我们发给 Anthropic）
//   - 本文件是"入口侧"：Anthropic 客户端 -> OpenAI chat（我们收客户端请求）
// 两者方向相反，因此不能复用，但字段语义保持一致以便对照。

import (
	"encoding/json"
	"fmt"
	"strings"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// anthropicInboundContent 是 Anthropic content block（多态）。
// 只声明我们需要读取的字段；未知字段被忽略，不会报错。
type anthropicInboundContent struct {
	Type string `json:"type"`
	// type=text
	Text string `json:"text,omitempty"`
	// type=image
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url,omitempty"`
	} `json:"source,omitempty"`
	// type=tool_use
	Id    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// type=tool_result 的 content 可能是字符串或 block 数组，统一用 RawMessage 兜住
	ToolUseId string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	// type=thinking / redacted_thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type anthropicInboundMessage struct {
	Role    string                    `json:"role"`
	Content []anthropicInboundContent `json:"content"`
}

// UnmarshalJSON 让 content 同时接受两种官方形态：
//
//	"content": "hello"                                  （字符串简写）
//	"content": [{"type":"text","text":"hello"}]         （block 数组）
//
// 字符串简写在实际客户端（含 curl 示例、部分 SDK）里很常见，
// 只按数组解析会直接 400，必须在入口处兼容。
func (m *anthropicInboundMessage) UnmarshalJSON(data []byte) error {
	// 用一个中间结构把 content 先当 RawMessage 接住
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role

	trimmed := strings.TrimSpace(string(raw.Content))
	if trimmed == "" || trimmed == "null" {
		m.Content = nil
		return nil
	}
	// 字符串简写 -> 单个 text block
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw.Content, &s); err != nil {
			return err
		}
		m.Content = []anthropicInboundContent{{Type: "text", Text: s}}
		return nil
	}
	// 数组形态
	var blocks []anthropicInboundContent
	if err := json.Unmarshal(raw.Content, &blocks); err != nil {
		return err
	}
	m.Content = blocks
	return nil
}

type anthropicInboundTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

// AnthropicMessagesRequest 是 Anthropic Messages API 的请求体。
// 同时兼容 system 为字符串或 block 数组两种形态（新版 SDK 用数组）。
type AnthropicMessagesRequest struct {
	Model         string                    `json:"model"`
	Messages      []anthropicInboundMessage `json:"messages"`
	System        json.RawMessage           `json:"system,omitempty"`
	MaxTokens     int                       `json:"max_tokens,omitempty"`
	Stream        bool                      `json:"stream,omitempty"`
	Temperature   *float64                  `json:"temperature,omitempty"`
	TopP          *float64                  `json:"top_p,omitempty"`
	TopK          int                       `json:"top_k,omitempty"`
	StopSequences []string                  `json:"stop_sequences,omitempty"`
	Tools         []anthropicInboundTool    `json:"tools,omitempty"`
	ToolChoice    any                       `json:"tool_choice,omitempty"`
	Metadata      any                       `json:"metadata,omitempty"`
	// dyt-113/114: Anthropic 扩展思考与上下文管理。
	// 这两项必须一路带到出口适配器，否则上游会按"未开启"处理。
	Thinking          any `json:"thinking,omitempty"`
	ContextManagement any `json:"context_management,omitempty"`
}

// anthropicSystemToText 把 system 字段（字符串或 block 数组）压成纯文本。
func anthropicSystemToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	// 形态一：纯字符串
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return ""
	}
	// 形态二：block 数组（新版 SDK），只取 text block
	var blocks []anthropicInboundContent
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// anthropicBlocksToOpenAIContent 把 Anthropic content block 数组转成
// OpenAI 的 content 形态：纯文本 -> string；含图片 -> []any(image_url/text)。
// tool_use 与 tool_result 不在这里处理（由外层转成 tool_calls / role=tool）。
func anthropicBlocksToOpenAIContent(blocks []anthropicInboundContent) any {
	// 先探测是否含图片；不含图片且只有一个 text block 时直接返回字符串，
	// 保持与纯文本请求完全一致的形态（避免下游多态处理差异）。
	hasImage := false
	textCount := 0
	for _, b := range blocks {
		switch b.Type {
		case "image":
			hasImage = true
		case "text":
			textCount++
		}
	}
	if !hasImage && textCount <= 1 {
		for _, b := range blocks {
			if b.Type == "text" {
				return b.Text
			}
		}
		return ""
	}

	parts := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case "image":
			if b.Source == nil {
				continue
			}
			// Anthropic 图片源：base64（type=base64）或 URL（type=url）
			var url string
			if b.Source.Type == "url" && b.Source.URL != "" {
				url = b.Source.URL
			} else if b.Source.Data != "" {
				mediaType := b.Source.MediaType
				if mediaType == "" {
					mediaType = "image/png"
				}
				url = "data:" + mediaType + ";base64," + b.Source.Data
			}
			if url == "" {
				continue
			}
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": url},
			})
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return parts
}

// toolResultContentToText 提取 tool_result 的文本内容。
// Anthropic 允许 content 为字符串或 block 数组，两种都支持。
func toolResultContentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return ""
	}
	var blocks []anthropicInboundContent
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// 无法解析时原样返回，至少不丢内容
		return trimmed
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// anthropicToChatRequest 把 Anthropic Messages 请求转换为 OpenAI chat 请求。
//
// 映射要点：
//   - system（string 或 block[]）            -> messages[0].role="system"
//   - user/assistant content block[]         -> content（string 或 image parts）
//   - assistant 的 tool_use block            -> tool_calls
//   - user 的 tool_result block              -> role="tool" + tool_call_id
//   - max_tokens                             -> max_tokens（Anthropic 必填，缺省补 4096）
//   - stop_sequences                         -> stop
//   - tools[].input_schema                   -> tools[].function.parameters
func anthropicToChatRequest(req *AnthropicMessagesRequest) *relaymodel.GeneralOpenAIRequest {
	out := &relaymodel.GeneralOpenAIRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopK:        req.TopK,
		// dyt-114: 扩展思考与上下文管理透传（OpenAI 通道会忽略这两个字段，
		// Anthropic 通道会真正带给上游）
		Thinking:          req.Thinking,
		ContextManagement: req.ContextManagement,
	}
	// Anthropic 的 max_tokens 是必填项；缺省时给一个安全值，避免上游报错
	if req.MaxTokens > 0 {
		out.MaxTokens = req.MaxTokens
	} else {
		out.MaxTokens = 4096
	}
	if len(req.StopSequences) == 1 {
		out.Stop = req.StopSequences[0]
	} else if len(req.StopSequences) > 1 {
		stop := make([]any, 0, len(req.StopSequences))
		for _, s := range req.StopSequences {
			stop = append(stop, s)
		}
		out.Stop = stop
	}

	// tools 转换
	if len(req.Tools) > 0 {
		tools := make([]relaymodel.Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Name == "" {
				continue
			}
			tools = append(tools, relaymodel.Tool{
				Type: "function",
				Function: relaymodel.Function{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
		if len(tools) > 0 {
			out.Tools = tools
			out.ToolChoice = convertAnthropicToolChoice(req.ToolChoice)
		}
	}

	messages := make([]relaymodel.Message, 0, len(req.Messages)+1)
	if sys := anthropicSystemToText(req.System); sys != "" {
		messages = append(messages, relaymodel.Message{Role: "system", Content: sys})
	}

	for _, m := range req.Messages {
		role := m.Role
		switch role {
		case "user", "assistant":
		default:
			// 未知角色按 user 处理，避免整条请求失败
			role = "user"
		}

		// 分离 tool_use（assistant）与 tool_result（user），其余作为常规内容
		var toolCalls []relaymodel.Tool
		plainBlocks := make([]anthropicInboundContent, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				if b.Id == "" || b.Name == "" {
					continue
				}
				// Anthropic 的 input 是对象，OpenAI 的 arguments 是 JSON 字符串
				args := "{}"
				if len(b.Input) > 0 {
					raw := strings.TrimSpace(string(b.Input))
					if raw != "" && raw != "null" {
						args = raw
					}
				}
				toolCalls = append(toolCalls, relaymodel.Tool{
					Id:   b.Id,
					Type: "function",
					Function: relaymodel.Function{
						Name:      b.Name,
						Arguments: args,
					},
				})
			case "thinking", "redacted_thinking":
				// 思考块不透传给 OpenAI 通道（token 已计入 usage，内容无用）
			case "tool_result":
				// 单独处理成 role="tool" 消息，不能混进常规 content
			default:
				plainBlocks = append(plainBlocks, b)
			}
		}

		// tool_result 必须单独成 role="tool" 的消息，且要排在常规内容之前，
		// 这样 tool 消息紧随发起它的 assistant tool_calls。
		toolResults := make([]relaymodel.Message, 0, len(m.Content))
		for _, b := range m.Content {
			if b.Type != "tool_result" {
				continue
			}
			toolResults = append(toolResults, relaymodel.Message{
				Role:       "tool",
				ToolCallId: b.ToolUseId,
				Content:    toolResultContentToText(b.Content),
			})
		}

		// 只有当确实产出内容时才发消息。
		// 注意：tool_result-only 的 user 消息会走 plainBlocks 为空的分支，
		// 若在这里无条件 append，会多出一条空 content 的 user 消息，
		// 部分上游会因此报 "content must not be empty"。
		hasPlainContent := len(plainBlocks) > 0
		if hasPlainContent || len(toolCalls) > 0 {
			content := anthropicBlocksToOpenAIContent(plainBlocks)
			// 纯 tool_calls 的 assistant 消息，content 用空字符串占位（避免 null）
			if !hasPlainContent && len(toolCalls) > 0 {
				content = ""
			}
			messages = append(messages, relaymodel.Message{
				Role:      role,
				Content:   content,
				ToolCalls: toolCalls,
			})
		}
		messages = append(messages, toolResults...)
	}

	out.Messages = messages
	return out
}

// convertAnthropicToolChoice 转换 tool_choice：
//
//	{"type":"auto"}                   -> "auto"
//	{"type":"any"}                    -> "required"
//	{"type":"tool","name":"x"}        -> {"type":"function","function":{"name":"x"}}
//	{"type":"none"}                   -> "none"
func convertAnthropicToolChoice(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return "auto"
	}
	t, _ := m["type"].(string)
	switch t {
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		name, _ := m["name"].(string)
		if name == "" {
			return "auto"
		}
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name},
		}
	case "auto":
		return "auto"
	default:
		return "auto"
	}
}

// anthropicStopReasonFromFinishReason 把 OpenAI finish_reason 映射回 Anthropic
// stop_reason，保证原生客户端拿到它预期的枚举值。
func anthropicStopReasonFromFinishReason(finishReason string) string {
	switch finishReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

// ensureAnthropicModel 在模型名为空时给出明确错误（Anthropic 客户端必传 model）
func ensureAnthropicModel(req *AnthropicMessagesRequest) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	return nil
}
