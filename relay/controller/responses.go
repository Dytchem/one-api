package controller

// dyt-53: OpenAI Responses API 支持。
// 请求方向：Responses → Chat Completions（上游全部是 chat API）
// 响应方向：Chat Completions → Responses（非流式 JSON + 流式 SSE）

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/random"
	"github.com/songquanpeng/one-api/common/render"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// OpenAIResponsesRequest 客户端传入的 Responses API 请求（只需解析需要的字段）
type OpenAIResponsesRequest struct {
	Model              string   `json:"model"`
	Input              any      `json:"input"`
	Instructions       string   `json:"instructions"`
	MaxOutputTokens    int      `json:"max_output_tokens"`
	Temperature        *float64 `json:"temperature"`
	TopP               *float64 `json:"top_p"`
	Stream             bool     `json:"stream"`
	ToolChoice         any      `json:"tool_choice"`
	Tools              any      `json:"tools"`
	Stop               any      `json:"stop"`
	PreviousResponseID string   `json:"previous_response_id"`
}

// responsesToChatRequest 把 Responses 请求转换为 Chat Completions 请求
func responsesToChatRequest(req *OpenAIResponsesRequest) *relaymodel.GeneralOpenAIRequest {
	chat := &relaymodel.GeneralOpenAIRequest{
		Model:    req.Model,
		Stream:   req.Stream,
		Messages: []relaymodel.Message{},
	}

	if req.Instructions != "" {
		chat.Messages = append(chat.Messages, relaymodel.Message{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	// input: string | array
	switch input := req.Input.(type) {
	case string:
		if input != "" {
			chat.Messages = append(chat.Messages, relaymodel.Message{
				Role:    "user",
				Content: input,
			})
		}
	case []any:
		for _, raw := range input {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			msgType, _ := item["type"].(string)
			switch msgType {
			case "message":
				role, _ := item["role"].(string)
				if role == "" {
					role = "user"
				}
				var textParts []string
				switch content := item["content"].(type) {
				case string:
					if content != "" {
						textParts = append(textParts, content)
					}
				case []any:
					for _, partRaw := range content {
						part, ok := partRaw.(map[string]any)
						if !ok {
							continue
						}
						partType, _ := part["type"].(string)
						switch partType {
						case "input_text", "output_text", "text":
							if t, ok := part["text"].(string); ok && t != "" {
								textParts = append(textParts, t)
							}
						case "input_image":
							textParts = append(textParts, "[image]")
						}
					}
				}
				if len(textParts) > 0 {
					chat.Messages = append(chat.Messages, relaymodel.Message{
						Role:    role,
						Content: strings.Join(textParts, "\n"),
					})
				}
			case "function_call":
				name, _ := item["name"].(string)
				args, _ := item["arguments"].(string)
				chat.Messages = append(chat.Messages, relaymodel.Message{
					Role: "assistant",
					ToolCalls: []relaymodel.Tool{{
						Id:   "call_" + random.GetRandomString(16),
						Type: "function",
						Function: relaymodel.Function{
							Name:      name,
							Arguments: args,
						},
					}},
				})
			case "function_call_output":
				callID, _ := item["call_id"].(string)
				outputStr := ""
				switch output := item["output"].(type) {
				case string:
					outputStr = output
				default:
					if b, err := json.Marshal(output); err == nil {
						outputStr = string(b)
					}
				}
				chat.Messages = append(chat.Messages, relaymodel.Message{
					Role:       "tool",
					ToolCallId: callID,
					Content:    outputStr,
				})
			}
		}
	}

	if req.MaxOutputTokens > 0 {
		chat.MaxTokens = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		chat.Temperature = req.Temperature
	}
	if req.TopP != nil {
		chat.TopP = req.TopP
	}
	if req.Stop != nil {
		chat.Stop = req.Stop
	}
	if req.Tools != nil {
		if tools, ok := req.Tools.([]any); ok {
			for _, tRaw := range tools {
				tMap, ok := tRaw.(map[string]any)
				if !ok {
					continue
				}
				// Responses 格式: {"type":"function","name":...,"description":...,"parameters":...}
				// 转换为 chat 格式: {"type":"function","function":{"name":...,"description":...,"parameters":...}}
				if fn, ok := tMap["name"].(string); ok && fn != "" {
					tool := relaymodel.Tool{
						Type: "function",
						Function: relaymodel.Function{
							Name:        fn,
							Description: fmt.Sprintf("%v", tMap["description"]),
							Parameters:  tMap["parameters"],
						},
					}
					chat.Tools = append(chat.Tools, tool)
				} else if tBytes, err := json.Marshal(tMap); err == nil {
					// 已经是 chat 格式（含 function 字段）
					var tool relaymodel.Tool
					if json.Unmarshal(tBytes, &tool) == nil && tool.Function.Name != "" {
						chat.Tools = append(chat.Tools, tool)
					}
				}
			}
		}
	}
	if req.ToolChoice != nil {
		chat.ToolChoice = req.ToolChoice
	}
	return chat
}

// responsesUsage Responses 协议的 usage 结构
type responsesUsage struct {
	InputTokens         int            `json:"input_tokens"`
	InputTokensDetails  map[string]int `json:"input_tokens_details"`
	OutputTokens        int            `json:"output_tokens"`
	OutputTokensDetails map[string]int `json:"output_tokens_details"`
	TotalTokens         int            `json:"total_tokens"`
}

func toResponsesUsage(u *relaymodel.Usage) responsesUsage {
	ru := responsesUsage{
		InputTokens:         u.PromptTokens,
		OutputTokens:        u.CompletionTokens,
		TotalTokens:         u.TotalTokens,
		InputTokensDetails:  map[string]int{},
		OutputTokensDetails: map[string]int{},
	}
	if u.PromptTokensDetails != nil {
		ru.InputTokensDetails["cached_tokens"] = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		ru.OutputTokensDetails["reasoning_tokens"] = u.CompletionTokensDetails.ReasoningTokens
	}
	return ru
}

// renderResponsesNonStream 把上游 chat JSON 响应转换为 Responses JSON 写回客户端。
// 返回上游 usage（供日志）。promptTokens 用于 usage 缺失时兜底估算。
func renderResponsesNonStream(c *gin.Context, chatBody []byte, modelName string, promptTokens int) *relaymodel.Usage {
	var chatResp openai.SlimTextResponse
	if err := json.Unmarshal(chatBody, &chatResp); err != nil {
		logger.SysError("responses: unmarshal chat response failed: " + err.Error())
		// 无法解析时按错误响应处理
		renderResponsesError(c, "invalid_upstream_response", "上游返回了无法解析的响应", http.StatusInternalServerError)
		return nil
	}
	if chatResp.Error.Type != "" {
		// 上游业务错误：转成 Responses 错误格式
		renderResponsesError(c, chatResp.Error.Code, chatResp.Error.Message, http.StatusBadGateway)
		return nil
	}

	msgID := "msg_" + random.GetRandomString(16)
	var fullText strings.Builder
	var outputs []json.RawMessage

	for _, choice := range chatResp.Choices {
		text := choice.Message.StringContent()
		if text != "" {
			fullText.WriteString(text)
		}
		for _, tc := range choice.Message.ToolCalls {
			callID := tc.Id
			if callID == "" {
				callID = "call_" + random.GetRandomString(16)
			}
			argsStr := ""
			if tc.Function.Arguments != nil {
				argsStr = fmt.Sprintf("%v", tc.Function.Arguments)
			}
			callItem := map[string]any{
				"id":        "fc_" + random.GetRandomString(16),
				"type":      "function_call",
				"status":    "completed",
				"call_id":   callID,
				"name":      tc.Function.Name,
				"arguments": argsStr,
			}
			raw, _ := json.Marshal(callItem)
			outputs = append(outputs, raw)
		}
	}
	if fullText.Len() > 0 || len(outputs) == 0 {
		msgItem := map[string]any{
			"id":     msgID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []map[string]any{{
				"type":        "output_text",
				"text":        fullText.String(),
				"annotations": []any{},
			}},
		}
		raw, _ := json.Marshal(msgItem)
		outputs = append(outputs, raw)
	}

	usage := chatResp.Usage
	if usage.TotalTokens == 0 && fullText.Len() > 0 {
		// 上游没返回 usage 时按文本估算
		usage = relaymodel.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: openai.CountTokenText(fullText.String(), modelName),
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	resp := map[string]any{
		"id":          "resp_" + random.GetRandomString(16),
		"object":      "response",
		"created_at":  float64(time.Now().UnixNano()) / 1e9,
		"status":      "completed",
		"model":       modelName,
		"output":      outputs,
		"usage":       toResponsesUsage(&usage),
		"temperature": 1,
		"top_p":       1,
	}
	body, _ := json.Marshal(resp)
	c.Writer.Write(body)
	return &usage
}

// renderResponsesError 输出 Responses 格式的错误响应
func renderResponsesError(c *gin.Context, code any, message string, statusCode int) {
	resp := map[string]any{
		"id":     "resp_" + random.GetRandomString(16),
		"object": "response",
		"status": "failed",
		"error": map[string]any{
			"type":    "server_error",
			"message": message,
			"code":    code,
		},
	}
	body, _ := json.Marshal(resp)
	c.Writer.WriteHeader(statusCode)
	c.Writer.Write(body)
}

// ---- 流式 Responses SSE ----

// responsesToolCallState 单个 function_call 的流式状态
type responsesToolCallState struct {
	id        string // fc_xxx（item id）
	callID    string
	name      string
	arguments strings.Builder
	added     bool
	done      bool
}

// responsesStreamState 流式转换状态机
type responsesStreamState struct {
	id        string
	msgID     string
	started   bool
	gotText   bool
	textDone  bool
	completed bool
	text      strings.Builder
	usage     *relaymodel.Usage
	model     string
	toolCalls map[string]*responsesToolCallState
}

func newResponsesStreamState(modelName string) *responsesStreamState {
	return &responsesStreamState{
		id:        "resp_" + random.GetRandomString(16),
		msgID:     "msg_" + random.GetRandomString(16),
		model:     modelName,
		toolCalls: map[string]*responsesToolCallState{},
	}
}

// writeEvent 写 SSE 事件：event: 行 + data: 行
func (s *responsesStreamState) writeEvent(c *gin.Context, eventType string, data any) {
	raw, _ := json.Marshal(data)
	render.StringEventData(c, eventType, string(raw))
}

// emitCreated 发送会话建立事件（任意输出出现前）
func (s *responsesStreamState) emitCreated(c *gin.Context) {
	if s.started {
		return
	}
	s.started = true
	s.writeEvent(c, "response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": s.id, "object": "response", "status": "in_progress",
			"model": s.model, "output": []any{},
		},
	})
	s.writeEvent(c, "response.in_progress", map[string]any{"type": "response.in_progress"})
}

// emitTextItemAdded 添加 message output item（首个文本 delta 前）
func (s *responsesStreamState) emitTextItemAdded(c *gin.Context) {
	s.writeEvent(c, "response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": 0,
		"item": map[string]any{
			"id": s.msgID, "type": "message", "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	})
	s.writeEvent(c, "response.content_part.added", map[string]any{
		"type": "response.content_part.added", "item_id": s.msgID,
		"output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

// emitContentDelta 增量输出文本
func (s *responsesStreamState) emitContentDelta(c *gin.Context, delta string) {
	if !s.started {
		s.emitCreated(c)
	}
	if !s.gotText {
		s.gotText = true
		s.emitTextItemAdded(c)
	}
	s.text.WriteString(delta)
	s.writeEvent(c, "response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": s.msgID,
		"output_index": 0, "content_index": 0, "delta": delta,
	})
}

// emitTextDone 文本输出完成
func (s *responsesStreamState) emitTextDone(c *gin.Context) {
	if s.textDone {
		return
	}
	s.textDone = true
	if !s.gotText {
		// 没有文本输出（纯 tool call）：不发送 text done，但 item 未添加过也无需补
		return
	}
	text := s.text.String()
	s.writeEvent(c, "response.output_text.done", map[string]any{
		"type": "response.output_text.done", "item_id": s.msgID,
		"output_index": 0, "content_index": 0, "text": text,
	})
	s.writeEvent(c, "response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": 0,
		"item": map[string]any{
			"id": s.msgID, "type": "message", "status": "completed",
			"role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		},
	})
}

// emitToolCallAdded 添加 function_call output item（首个 delta 前）
func (s *responsesStreamState) emitToolCallAdded(c *gin.Context, tc *responsesToolCallState) {
	if !s.started {
		s.emitCreated(c)
	}
	tc.added = true
	s.writeEvent(c, "response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": len(s.toolCalls),
		"item": map[string]any{
			"id": tc.id, "type": "function_call", "status": "in_progress",
			"call_id": tc.callID, "name": tc.name, "arguments": "",
		},
	})
}

// emitToolCallDelta 函数参数增量
func (s *responsesStreamState) emitToolCallDelta(c *gin.Context, tc *responsesToolCallState, delta string) {
	tc.arguments.WriteString(delta)
	s.writeEvent(c, "response.function_call_arguments.delta", map[string]any{
		"type": "response.function_call_arguments.delta", "item_id": tc.id,
		"output_index": 0, "delta": delta,
	})
}

// emitToolCallDone 函数调用完成
func (s *responsesStreamState) emitToolCallDone(c *gin.Context, tc *responsesToolCallState) {
	if tc.done {
		return
	}
	tc.done = true
	s.writeEvent(c, "response.function_call_arguments.done", map[string]any{
		"type": "response.function_call_arguments.done", "item_id": tc.id,
		"output_index": 0, "arguments": tc.arguments.String(),
	})
	s.writeEvent(c, "response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": 0,
		"item": map[string]any{
			"id": tc.id, "type": "function_call", "status": "completed",
			"call_id": tc.callID, "name": tc.name, "arguments": tc.arguments.String(),
		},
	})
}

// emitCompleted 整个响应完成
func (s *responsesStreamState) emitCompleted(c *gin.Context) {
	if s.completed {
		return
	}
	s.completed = true
	s.emitTextDone(c)
	for _, tc := range s.toolCalls {
		s.emitToolCallDone(c, tc)
	}
	var usage any
	if s.usage != nil {
		usage = toResponsesUsage(s.usage)
	}
	var outputs []any
	if s.gotText {
		text := s.text.String()
		outputs = append(outputs, map[string]any{
			"id": s.msgID, "type": "message", "status": "completed",
			"role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		})
	}
	for _, tc := range s.toolCalls {
		outputs = append(outputs, map[string]any{
			"id": tc.id, "type": "function_call", "status": "completed",
			"call_id": tc.callID, "name": tc.name, "arguments": tc.arguments.String(),
		})
	}
	s.writeEvent(c, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": s.id, "object": "response", "status": "completed",
			"model": s.model, "error": nil, "incomplete_details": nil,
			"output": outputs,
			"usage":  usage,
		},
	})
}

// feedLine 转换一条上游 chat SSE 行
func (s *responsesStreamState) feedLine(c *gin.Context, data string) {
	if len(data) < 6 || data[:6] != "data: " {
		return
	}
	payload := strings.TrimSpace(data[6:])
	if payload == "[DONE]" {
		s.emitCompleted(c)
		return
	}
	var chunk openai.ChatCompletionsStreamResponse
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	for _, choice := range chunk.Choices {
		hasFinish := choice.FinishReason != nil && *choice.FinishReason != ""
		// 内容 delta
		var content string
		switch v := choice.Delta.Content.(type) {
		case string:
			content = v
		}
		if content == "" {
			if rc, ok := choice.Delta.ReasoningContent.(string); ok {
				content = rc
			}
		}
		// tool calls delta（始终按 index 归并——DeepSeek/OpenAI 首个 chunk 带 id，
		// 后续 delta 只带 index 不带 id）
		var toolDelta []struct {
			key  string
			id   string
			name string
			args string
		}
		for _, tc := range choice.Delta.ToolCalls {
			argsStr := ""
			if tc.Function.Arguments != nil {
				argsStr = fmt.Sprintf("%v", tc.Function.Arguments)
			}
			key := fmt.Sprintf("idx%d", tc.Index)
			toolDelta = append(toolDelta, struct {
				key  string
				id   string
				name string
				args string
			}{key: key, id: tc.Id, name: tc.Function.Name, args: argsStr})
		}

		if content != "" {
			s.emitContentDelta(c, content)
		}
		for _, td := range toolDelta {
			tc, ok := s.toolCalls[td.key]
			if !ok {
				tc = &responsesToolCallState{
					id:     "fc_" + random.GetRandomString(16),
					callID: td.id,
					name:   td.name,
				}
				s.toolCalls[td.key] = tc
				s.emitToolCallAdded(c, tc)
			}
			if td.id != "" {
				tc.callID = td.id
			}
			if td.name != "" {
				tc.name = td.name
			}
			if td.args != "" {
				s.emitToolCallDelta(c, tc, td.args)
			}
		}
		if hasFinish {
			s.emitTextDone(c)
			for _, tc := range s.toolCalls {
				s.emitToolCallDone(c, tc)
			}
		}
	}
}

// finishUp 流结束时补发缺失事件（无 [DONE] 时由调用方触发）
func (s *responsesStreamState) finishUp(c *gin.Context) {
	if !s.started && len(s.toolCalls) == 0 {
		s.emitCreated(c)
	}
	s.emitCompleted(c)
}

// hasOutput 是否已有任何输出
func (s *responsesStreamState) hasOutput() bool {
	return s.gotText || len(s.toolCalls) > 0
}
