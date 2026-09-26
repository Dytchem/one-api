package controller

// dyt-113: Gemini Interactions API 原生入口（POST /v1beta/interactions）。
//
// 背景：Google 已于 2026-06 将 Interactions API 正式 GA，并作为 Gemini 模型的
// 主推统一入口（替代旧 generateContent / streamGenerateContent）。旧版 one-api
// 只有 gemini 通道的"出口侧"适配（把 OpenAI chat 转成 generateContent 发出去），
// 没有入口侧支持，导致按新文档写的客户端无法直接打网关。
//
// 本文件实现入口侧：Interactions 请求 -> OpenAI chat 请求。
// 支持字段（对齐官方文档 https://ai.google.dev/api/interactions-api）：
//   - model / agent（二者其一）
//   - input：字符串、或 block 数组（text / image / user_input / model_output）
//   - previous_interaction_id：服务端状态，网关侧转为本地会话缓存拼接
//   - tools：google_search / mcp_server / computer_use 等 —— 网关无法代持这些
//     服务端工具，显式拒绝以免静默降级产生错误结果
//   - background / environment：异步与远端执行，网关不代持，显式拒绝
//   - stream：映射到 OpenAI stream

import (
	"encoding/json"
	"fmt"
	"strings"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// geminiInteractionContent 是 Interactions input 数组里的内容块。
type geminiInteractionContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// geminiInteractionInput 是 input 数组的一个元素。
// 新版 API 用 {type:"user_input", content:[...]} / {type:"model_output", content:[...]}
// 也兼容直接把 {type:"text"} / {type:"image"} 放在数组里。
type geminiInteractionInput struct {
	Type    string                     `json:"type"`
	Content []geminiInteractionContent `json:"content,omitempty"`
	// 扁平形态（数组元素直接是 text/image block 时）
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// GeminiInteractionsRequest 是 Interactions API 的请求体。
type GeminiInteractionsRequest struct {
	Model  string          `json:"model,omitempty"`
	Agent  string          `json:"agent,omitempty"`
	Input  json.RawMessage `json:"input"`
	Stream bool            `json:"stream,omitempty"`
	// 服务端状态：本轮引用上一轮的 interaction id
	PreviousInteractionID string `json:"previous_interaction_id,omitempty"`
	// 服务端工具与执行环境（网关不代持）
	Tools       []any           `json:"tools,omitempty"`
	Background  bool            `json:"background,omitempty"`
	Environment json.RawMessage `json:"environment,omitempty"`
	// 生成参数
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	MaxOutputTokens  int      `json:"max_output_tokens,omitempty"`
	ResponseMimeType string   `json:"response_mime_type,omitempty"`
	// 系统指令
	SystemInstruction json.RawMessage `json:"system_instruction,omitempty"`
}

// geminiInteractionTextBlock 从任意形态的内容块提取文本/图片部分。
func geminiContentToParts(blocks []geminiInteractionContent) (string, []any) {
	var sb strings.Builder
	images := make([]any, 0)
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			if b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(b.Text)
			}
		case "image", "image_url":
			url := ""
			if b.URI != "" {
				url = b.URI
			} else if b.Data != "" {
				mt := b.MimeType
				if mt == "" {
					mt = "image/png"
				}
				url = "data:" + mt + ";base64," + b.Data
			}
			if url != "" {
				images = append(images, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": url},
				})
			}
		default:
			// 未识别类型：若有 text 就取，尽量不丢内容
			if b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(b.Text)
			}
		}
	}
	return sb.String(), images
}

// geminiSystemInstructionToText 解析 system_instruction，兼容字符串与
// {parts:[{text:"..."}]} 两种官方形态。
func geminiSystemInstructionToText(raw json.RawMessage) string {
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
	var obj struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Parts) > 0 {
		parts := make([]string, 0, len(obj.Parts))
		for _, p := range obj.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// parseGeminiInput 把 Interactions 的 input 解析为一组 chat 消息。
// 返回的消息只含本轮内容；历史由 previous_interaction_id 在调用方拼接。
func parseGeminiInput(raw json.RawMessage) ([]relaymodel.Message, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("input is required")
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return nil, fmt.Errorf("input is required")
	}

	// 形态一：纯字符串
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("invalid input string: %w", err)
		}
		return []relaymodel.Message{{Role: "user", Content: s}}, nil
	}

	// 形态二：数组
	var items []geminiInteractionInput
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("invalid input array: %w", err)
	}
	messages := make([]relaymodel.Message, 0, len(items))
	// 扁平 block 会连续出现（text 后紧跟 image），它们属于同一条用户消息，
	// 必须合并而不是各自成条，否则上游会看到"只有图没有问句"的畸形对话。
	var pendingFlat []geminiInteractionContent

	flushFlat := func() {
		if len(pendingFlat) == 0 {
			return
		}
		text, images := geminiContentToParts(pendingFlat)
		var content any = text
		if len(images) > 0 {
			parts := make([]any, 0, len(images)+1)
			if text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
			parts = append(parts, images...)
			content = parts
		}
		messages = append(messages, relaymodel.Message{Role: "user", Content: content})
		pendingFlat = pendingFlat[:0]
	}

	for _, it := range items {
		switch it.Type {
		case "user_input", "model_output":
			flushFlat()
			role := "user"
			if it.Type == "model_output" {
				role = "assistant"
			}
			text, images := geminiContentToParts(it.Content)
			var content any = text
			if len(images) > 0 {
				parts := make([]any, 0, len(images)+1)
				if text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": text})
				}
				parts = append(parts, images...)
				content = parts
			}
			messages = append(messages, relaymodel.Message{Role: role, Content: content})
		case "text", "image", "image_url", "":
			// 扁平形态：累积到同一条用户消息
			pendingFlat = append(pendingFlat, geminiInteractionContent{
				Type: it.Type, Text: it.Text, Data: it.Data, MimeType: it.MimeType, URI: it.URI,
			})
		default:
			return nil, fmt.Errorf("unsupported input block type %q", it.Type)
		}
	}
	flushFlat()
	if len(messages) == 0 {
		return nil, fmt.Errorf("input produced no messages")
	}
	return messages, nil
}

// geminiInteractionsToChatRequest 把 Interactions 请求转换为 OpenAI chat 请求。
// history 为 previous_interaction_id 对应的历史消息（可为空）。
func geminiInteractionsToChatRequest(req *GeminiInteractionsRequest, history []relaymodel.Message) (*relaymodel.GeneralOpenAIRequest, error) {
	model := req.Model
	if model == "" {
		model = req.Agent
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model or agent is required")
	}

	// 网关无法代持的服务端能力：显式拒绝，避免静默降级给出错误结果
	if len(req.Tools) > 0 {
		return nil, fmt.Errorf("server-side tools (google_search/mcp_server/computer_use) are not supported by this gateway; use the OpenAI-compatible endpoint instead")
	}
	if req.Background {
		return nil, fmt.Errorf("background execution is not supported by this gateway")
	}
	if len(req.Environment) > 0 && strings.TrimSpace(string(req.Environment)) != "null" {
		return nil, fmt.Errorf("remote environment execution is not supported by this gateway")
	}

	out := &relaymodel.GeneralOpenAIRequest{
		Model:       model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}
	if req.MaxOutputTokens > 0 {
		out.MaxTokens = req.MaxOutputTokens
	}

	current, err := parseGeminiInput(req.Input)
	if err != nil {
		return nil, err
	}

	messages := make([]relaymodel.Message, 0, len(history)+len(current)+1)
	if sys := geminiSystemInstructionToText(req.SystemInstruction); sys != "" {
		messages = append(messages, relaymodel.Message{Role: "system", Content: sys})
	}
	messages = append(messages, history...)
	messages = append(messages, current...)
	out.Messages = messages
	return out, nil
}
