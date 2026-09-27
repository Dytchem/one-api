package controller

// dyt-116: 出口为 Responses API 的渠道，其流式响应是 Responses SSE 事件格式，
// 与 OpenAI chat SSE 完全不同。网关对客户端必须统一输出 chat SSE，
// 同时流式探测也需要能识别"首个有效 token"。
//
// 背景（v114 引入的真实故障）：Chat/Agent 页面一律用流式请求。流式路径会先走
// 探测（isProbeCompatible 对 OpenAI 类型渠道恒为 true），而探测只认
// `data: {"choices":[...]}`。Responses 上游发的是
//   event: response.output_text.delta
//   data: {"type":"response.output_text.delta","delta":"PONG",...}
// 探测永远匹配不到 content → 判定 "all N probe attempts returned empty response"
// → 返回 502。表现为"Chat/Agent 打不开，但 curl 非流式却能通"。
//
// 本文件把 Responses SSE 转成 chat SSE，供探测识别与客户端消费。

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/render"
)

// responsesSSEEvent 是 Responses SSE 事件的最小可读结构。
// 只声明我们需要的字段；未知字段忽略。
type responsesSSEEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemId string `json:"item_id"`
	// response.completed / response.failed 里携带完整响应与错误
	Response *struct {
		Id     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Usage  *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	} `json:"response"`
	// 工具调用增量
	Arguments string `json:"arguments"`
	Item      *struct {
		Type   string `json:"type"`
		CallId string `json:"call_id"`
		Name   string `json:"name"`
		Id     string `json:"id"`
	} `json:"item"`
}

// responsesToChatStreamState 负责把 Responses SSE 转成 chat SSE。
type responsesToChatStreamState struct {
	id       string
	model    string
	started  bool
	doneSent bool
	usage    *relaymodelUsage
}

// relaymodelUsage 避免与 relay/model 包重名，仅承载 token 计数
type relaymodelUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func newResponsesToChatStreamState(modelName string) *responsesToChatStreamState {
	return &responsesToChatStreamState{model: modelName}
}

// chatChunk 组装一条 chat SSE data 负载
func (s *responsesToChatStreamState) chatChunk(delta map[string]any, finishReason any) []byte {
	if s.id == "" {
		s.id = "chatcmpl-stream"
	}
	choices := []any{}
	if delta != nil || finishReason != nil {
		choices = append(choices, map[string]any{
			"index": 0, "delta": delta, "finish_reason": finishReason,
		})
	}
	payload := map[string]any{
		"id": s.id, "object": "chat.completion.chunk", "created": 0,
		"model": s.model, "choices": choices,
	}
	if s.usage != nil {
		payload["usage"] = map[string]any{
			"prompt_tokens":     s.usage.PromptTokens,
			"completion_tokens": s.usage.CompletionTokens,
			"total_tokens":      s.usage.TotalTokens,
		}
	}
	b, _ := json.Marshal(payload)
	return b
}

// feed 处理一条 Responses SSE 原始行，返回应写给客户端的 chat SSE 负载。
// 返回 nil 表示该行无输出（大多数事件都是这种）。
//
// 关键：必须把 delta 文本包装成 `{"choices":[{"delta":{"content":"..."}}]}`
// 形状，否则流式探测无法确认"首个 token"，Chat/Agent 会拿到 502。
func (s *responsesToChatStreamState) feed(line string) []byte {
	// 取 data: 负载
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return nil
	}
	var ev responsesSSEEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "response.created", "response.in_progress":
		// 记录 id / model，先发一个 role 起始块（与 OpenAI 行为一致）
		if ev.Response != nil {
			if ev.Response.Id != "" {
				s.id = ev.Response.Id
			}
			if ev.Response.Model != "" {
				s.model = ev.Response.Model
			}
		}
		if !s.started {
			s.started = true
			return s.chatChunk(map[string]any{"role": "assistant", "content": ""}, nil)
		}
		return nil

	case "response.output_text.delta":
		if !s.started {
			s.started = true
		}
		if ev.Delta == "" {
			return nil
		}
		return s.chatChunk(map[string]any{"content": ev.Delta}, nil)

	case "response.reasoning_summary_text.delta":
		// 推理摘要增量 → reasoning_content（OpenAI 兼容客户端可识别）
		if ev.Delta == "" {
			return nil
		}
		if !s.started {
			s.started = true
		}
		return s.chatChunk(map[string]any{"reasoning_content": ev.Delta}, nil)

	case "response.function_call_arguments.delta":
		if ev.Delta == "" {
			return nil
		}
		name := ""
		if ev.Item != nil {
			name = ev.Item.Name
		}
		return s.chatChunk(map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "type": "function",
				"function": map[string]any{"name": name, "arguments": ev.Delta},
			}},
		}, nil)

	case "response.completed":
		if ev.Response != nil && ev.Response.Usage != nil {
			s.usage = &relaymodelUsage{
				PromptTokens:     ev.Response.Usage.InputTokens,
				CompletionTokens: ev.Response.Usage.OutputTokens,
				TotalTokens:      ev.Response.Usage.TotalTokens,
			}
		}
		// 结束块：带 usage 的最终 chunk
		return s.chatChunk(map[string]any{}, "stop")

	case "response.failed", "error":
		msg := "upstream response failed"
		if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
			msg = ev.Response.Error.Message
		}
		return s.chatChunk(map[string]any{"content": "[upstream error] " + msg}, "stop")

	default:
		// 其它事件（output_item.added/done、content_part.* 等）无需转发
		return nil
	}
}

// writeChatChunk 把 chat SSE 负载写给客户端
func writeChatChunk(c *gin.Context, chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	render.StringData(c, string(chunk))
}
