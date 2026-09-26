package openai

// dyt-114: OpenAI Responses API 的"出口侧"适配（网关 -> 上游）。
//
// 背景：Responses 是 OpenAI 主推的新接口，且越来越多只提供 Responses 端点的
// 上游（或只在该端点支持 reasoning/tool 新特性）。而网关此前只能把客户端请求
// 转成 chat 格式发出去，遇到"只有 /v1/responses"的上游就打不通。
//
// 本文件把内部统一的 chat 请求转成 Responses 请求发给上游，
// 并把 Responses 响应转回 chat 格式，使网关对客户端保持统一输出。
//
// 协议依据：https://developers.openai.com/api/docs/guides/migrate-to-responses
//   POST {base}/v1/responses
//   { "model": "...", "input": [{"role":"user","content":[{"type":"input_text","text":".."}]}],
//     "instructions": "...", "max_output_tokens": N, "stream": bool }
//   响应：{ id, model, output: [{type:"message",content:[{type:"output_text",text}]},
//                              {type:"function_call",call_id,name,arguments}],
//           usage:{input_tokens,output_tokens,total_tokens} }

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/model"
)

// ---- 请求结构 ----

type responsesInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesInputItem struct {
	Type    string                  `json:"type,omitempty"`
	Role    string                  `json:"role,omitempty"`
	Content []responsesInputContent `json:"content,omitempty"`
	// function_call / function_call_output
	CallId    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
}

type responsesRequest struct {
	Model           string               `json:"model"`
	Input           []responsesInputItem `json:"input"`
	Instructions    string               `json:"instructions,omitempty"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Temperature     *float64             `json:"temperature,omitempty"`
	TopP            *float64             `json:"top_p,omitempty"`
	Stream          bool                 `json:"stream,omitempty"`
	Tools           []model.Tool         `json:"tools,omitempty"`
	ToolChoice      any                  `json:"tool_choice,omitempty"`
}

// ---- 响应结构 ----

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	// dyt-115: 其中有多少 output token 花在推理上
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesOutputItem struct {
	Type      string                   `json:"type"`
	Role      string                   `json:"role,omitempty"`
	Content   []responsesOutputContent `json:"content,omitempty"`
	CallId    string                   `json:"call_id,omitempty"`
	Id        string                   `json:"id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
	Status    string                   `json:"status,omitempty"`
	// dyt-115: type=reasoning 的摘要。推理模型（如 Muse Spark 1.3）在
	// max_output_tokens 较小时会把额度全用在推理上，message 项根本不会产生，
	// 导致客户端拿到空 content。这里把 summary 读出来，至少在无正文时
	// 能给客户端一个可解释的提示，而不是静默空响应。
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary,omitempty"`
}

type responsesResponse struct {
	Id     string                `json:"id"`
	Model  string                `json:"model"`
	Status string                `json:"status"`
	Output []responsesOutputItem `json:"output"`
	Usage  *responsesUsage       `json:"usage,omitempty"`
	// dyt-115: 上游因 token 预算耗尽而截断时的原因（如 max_output_tokens）
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

// ConvertRequestToResponses 把内部 chat 请求转换为 Responses 请求。
//
// 映射要点：
//   - system 消息                     -> instructions（Responses 是顶层字段）
//   - user/assistant 文本             -> input[] 的 message 项，content type=input_text
//   - content 里的图片                -> input_image
//   - assistant 的 tool_calls         -> input[] 的 function_call 项
//   - role=tool 的回执                -> input[] 的 function_call_output 项
//   - max_tokens                      -> max_output_tokens
func ConvertRequestToResponses(textRequest model.GeneralOpenAIRequest) *responsesRequest {
	req := &responsesRequest{
		Model:           textRequest.Model,
		Input:           make([]responsesInputItem, 0, len(textRequest.Messages)),
		MaxOutputTokens: textRequest.MaxTokens,
		Temperature:     textRequest.Temperature,
		TopP:            textRequest.TopP,
		Stream:          textRequest.Stream,
	}
	if len(textRequest.Tools) > 0 {
		req.Tools = textRequest.Tools
		req.ToolChoice = textRequest.ToolChoice
	}

	var sysParts []string
	for _, message := range textRequest.Messages {
		switch message.Role {
		case "system":
			if s := responsesMessageText(message); s != "" {
				sysParts = append(sysParts, s)
			}
		case "user", "assistant":
			content := responsesContentFromMessage(message)
			if len(content) > 0 {
				req.Input = append(req.Input, responsesInputItem{
					Type:    "message",
					Role:    message.Role,
					Content: content,
				})
			}
			// assistant 的 tool_calls 作为独立的 function_call 项
			for _, tc := range message.ToolCalls {
				args := "{}"
				if s, ok := tc.Function.Arguments.(string); ok && s != "" {
					args = s
				} else if tc.Function.Arguments != nil {
					if b, err := json.Marshal(tc.Function.Arguments); err == nil {
						args = string(b)
					}
				}
				req.Input = append(req.Input, responsesInputItem{
					Type:      "function_call",
					CallId:    tc.Id,
					Name:      tc.Function.Name,
					Arguments: json.RawMessage(args),
				})
			}
		case "tool":
			req.Input = append(req.Input, responsesInputItem{
				Type:   "function_call_output",
				CallId: message.ToolCallId,
				Output: responsesMessageText(message),
			})
		default:
			if s := responsesMessageText(message); s != "" {
				req.Input = append(req.Input, responsesInputItem{
					Type:    "message",
					Role:    "user",
					Content: []responsesInputContent{{Type: "input_text", Text: s}},
				})
			}
		}
	}

	if len(req.Input) == 0 {
		req.Input = append(req.Input, responsesInputItem{
			Type:    "message",
			Role:    "user",
			Content: []responsesInputContent{{Type: "input_text", Text: ""}},
		})
	}
	if len(sysParts) > 0 {
		req.Instructions = strings.Join(sysParts, "\n")
	}
	return req
}

// responsesContentFromMessage 把 chat 消息内容转成 Responses 输入内容块。
func responsesContentFromMessage(message model.Message) []responsesInputContent {
	if s, ok := message.Content.(string); ok {
		if s == "" {
			return nil
		}
		return []responsesInputContent{{Type: "input_text", Text: s}}
	}
	arr, ok := message.Content.([]any)
	if !ok {
		if s := responsesMessageText(message); s != "" {
			return []responsesInputContent{{Type: "input_text", Text: s}}
		}
		return nil
	}
	out := make([]responsesInputContent, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "text":
			if t, ok := m["text"].(string); ok && t != "" {
				out = append(out, responsesInputContent{Type: "input_text", Text: t})
			}
		case "image_url":
			iu, ok := m["image_url"].(map[string]any)
			if !ok {
				continue
			}
			if url, ok := iu["url"].(string); ok && url != "" {
				out = append(out, responsesInputContent{Type: "input_image", ImageURL: url})
			}
		}
	}
	return out
}

// responsesMessageText 提取消息纯文本。
func responsesMessageText(message model.Message) string {
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

// MergeNonStreamResponsesIntoChat 把非流式 Responses 响应合并为 chat 文本与工具调用。
// 返回 (文本, tool_calls, 上游 usage)。
func MergeNonStreamResponsesIntoChat(ir *responsesResponse) (string, []model.Tool, *responsesUsage) {
	var sb strings.Builder
	var reasoningSB strings.Builder
	toolCalls := make([]model.Tool, 0)
	for _, o := range ir.Output {
		switch o.Type {
		case "reasoning":
			// dyt-115: 推理摘要（多数实现只给 encrypted_content，summary 可能为空）
			for _, sm := range o.Summary {
				if sm.Text != "" {
					reasoningSB.WriteString(sm.Text)
				}
			}
		case "message":
			for _, c := range o.Content {
				if (c.Type == "output_text" || c.Type == "text") && c.Text != "" {
					sb.WriteString(c.Text)
				}
			}
		case "function_call":
			args := o.Arguments
			if args == "" {
				args = "{}"
			}
			id := o.CallId
			if id == "" {
				id = o.Id
			}
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
			for _, c := range o.Content {
				if c.Text != "" {
					sb.WriteString(c.Text)
				}
			}
		}
	}
	// dyt-115: 若上游只产出推理、没有任何正文与工具调用，说明推理吃光了
	// max_output_tokens（Muse Spark 1.3 在 300 token 预算下就是这种情况：
	// status 仍是 completed，但 output 里只有 reasoning，没有 message）。
	// 返回空字符串会让客户端完全无法判断发生了什么，因此给出明确提示。
	text := sb.String()
	if text == "" && len(toolCalls) == 0 {
		if reasoningSB.Len() > 0 {
			text = reasoningSB.String()
		} else if truncatedByReasoning(ir) {
			// 推理吃光预算且上游没给 summary：给出可操作的提示，
			// 否则客户端只看到空 content，无从判断这是配置问题。
			text = "[reasoning consumed the entire max_output_tokens budget before any output was produced; raise max_tokens]"
		}
	}
	return text, toolCalls, ir.Usage
}

// truncatedByReasoning 判断"有输出但全是推理、且因 token 预算截断"的情形。
// 典型场景：Muse Spark 1.3 在 max_tokens=300 时 reasoning_tokens≈300、无 message 项。
func truncatedByReasoning(ir *responsesResponse) bool {
	if ir.Usage == nil || ir.Usage.OutputTokensDetails.ReasoningTokens == 0 {
		return false
	}
	hasMessage := false
	for _, o := range ir.Output {
		if o.Type == "message" || o.Type == "function_call" {
			hasMessage = true
			break
		}
	}
	return !hasMessage
}

// ---- 响应处理（出口侧）----

// ResponsesHandler 解析上游 Responses 响应并转回 OpenAI chat 格式。
func ResponsesHandler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	_ = resp.Body.Close()

	var ir responsesResponse
	if err := json.Unmarshal(responseBody, &ir); err != nil {
		return ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}
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

	text, toolCalls, upstreamUsage := MergeNonStreamResponsesIntoChat(&ir)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	usage := model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: CountTokenText(text, modelName),
	}
	if upstreamUsage != nil {
		if upstreamUsage.InputTokens > 0 {
			usage.PromptTokens = upstreamUsage.InputTokens
		}
		if upstreamUsage.OutputTokens > 0 {
			usage.CompletionTokens = upstreamUsage.OutputTokens
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	out := TextResponse{
		Id:      ir.Id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []TextResponseChoice{{
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
	if out.Id == "" {
		out.Id = "chatcmpl-" + modelName
	}

	jsonResponse, err := json.Marshal(out)
	if err != nil {
		return ErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError), nil
	}
	return nil, &usage
}
