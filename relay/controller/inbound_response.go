package controller

// dyt-113: 原生入口的响应回写层。
//
// 思路：请求侧我们已把 Anthropic / Gemini 请求转成 OpenAI chat 请求，
// 于是整条 relay 流程（含重试、探测、计费、日志）都能原样复用。响应侧则
// 反过来把 OpenAI 格式的结果再编码回客户端期望的原生格式。
//
// 实现方式：用一层 transcodeWriter 包住 gin.ResponseWriter，在字节流出时
// 就地转码。相比改动各 adaptor 的 DoResponse，这种方式：
//   - 不碰既有 adaptor，零回归风险
//   - 自动覆盖流式与非流式两条路径
//   - 计费/日志用的是转码前的内容，不受影响
//
// 之所以可行：同为 SSE/JSON 的文本流，两种协议的语义一一对应。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// anthropicMessageID 由 OpenAI chatcmpl-xxx 映射成 Anthropic msg_xxx，保持可追溯
func anthropicMessageID(openAIID string) string {
	if openAIID == "" {
		return "msg_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if strings.HasPrefix(openAIID, "msg_") {
		return openAIID
	}
	// chatcmpl-abc -> msg_abc
	id := strings.TrimPrefix(openAIID, "chatcmpl-")
	return "msg_" + id
}

// openAIToAnthropicResponse 把非流式 OpenAI chat 响应体转成 Anthropic Message 响应。
func openAIToAnthropicResponse(body []byte, fallbackModel string) ([]byte, error) {
	var oai openai.TextResponse
	if err := json.Unmarshal(body, &oai); err != nil {
		// 上游返回了非 chat 结构（例如错误体），原样透传由客户端处理
		return body, nil
	}
	if len(oai.Choices) == 0 {
		return body, nil
	}
	choice := oai.Choices[0]

	blocks := make([]map[string]any, 0, 2)
	// 思考内容（若上游提供）放最前，符合 Anthropic 的 thinking block 顺序
	if s, ok := choice.Message.ReasoningContent.(string); ok && s != "" {
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": s})
	}
	if text := messageTextContent(choice.Message); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, tc := range choice.Message.ToolCalls {
		var input any = map[string]any{}
		if s, ok := tc.Function.Arguments.(string); ok && s != "" {
			_ = json.Unmarshal([]byte(s), &input)
		}
		name := tc.Function.Name
		id := tc.Id
		if id == "" {
			id = "toolu_" + fmt.Sprintf("%d", time.Now().UnixNano())
		}
		blocks = append(blocks, map[string]any{
			"type": "tool_use", "id": id, "name": name, "input": input,
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}

	model := oai.Model
	if model == "" {
		model = fallbackModel
	}
	out := map[string]any{
		"id":            anthropicMessageID(oai.Id),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       blocks,
		"stop_reason":   anthropicStopReasonFromFinishReason(choice.FinishReason),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  oai.Usage.PromptTokens,
			"output_tokens": oai.Usage.CompletionTokens,
		},
	}
	return json.Marshal(out)
}

// messageTextContent 提取 assistant 消息的纯文本内容。
func messageTextContent(msg relaymodel.Message) string {
	switch v := msg.Content.(type) {
	case string:
		return v
	case nil:
		return ""
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

// openAIToGeminiInteractionResponse 把非流式 OpenAI chat 响应转成 Interactions 响应。
// 真实 Interactions 响应的 details 层较复杂，这里给出与官方语义等价的最小结构：
// {id, model, output:[{type:"text",text:...}], usage:{...}}。
func openAIToGeminiInteractionResponse(body []byte, fallbackModel string) ([]byte, error) {
	var oai openai.TextResponse
	if err := json.Unmarshal(body, &oai); err != nil {
		return body, nil
	}
	if len(oai.Choices) == 0 {
		return body, nil
	}
	choice := oai.Choices[0]
	model := oai.Model
	if model == "" {
		model = fallbackModel
	}
	output := make([]any, 0, 2)
	if s, ok := choice.Message.ReasoningContent.(string); ok && s != "" {
		output = append(output, map[string]any{"type": "thought", "text": s})
	}
	if text := messageTextContent(choice.Message); text != "" {
		output = append(output, map[string]any{"type": "text", "text": text})
	}
	for _, tc := range choice.Message.ToolCalls {
		var args any = map[string]any{}
		if s, ok := tc.Function.Arguments.(string); ok && s != "" {
			_ = json.Unmarshal([]byte(s), &args)
		}
		output = append(output, map[string]any{
			"type": "function_call", "id": tc.Id, "name": tc.Function.Name, "arguments": args,
		})
	}

	id := oai.Id
	if id == "" {
		id = "int_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	out := map[string]any{
		"id":     id,
		"model":  model,
		"status": "completed",
		"output": output,
		"usage": map[string]any{
			"input_tokens":  oai.Usage.PromptTokens,
			"output_tokens": oai.Usage.CompletionTokens,
			"total_tokens":  oai.Usage.TotalTokens,
		},
	}
	return json.Marshal(out)
}

// ---- 流式转码 ----

// anthropicStreamState 保存一次 Anthropic 流式回写所需的跨事件状态。
type anthropicStreamState struct {
	messageID    string
	model        string
	started      bool
	blockIndex   int
	textOpen     bool
	toolOpens    map[int]bool // OpenAI tool_calls index -> 是否已发 content_block_start
	inputTokens  int
	outputTokens int
	stopReason   string
}

func newAnthropicStreamState(model string) *anthropicStreamState {
	return &anthropicStreamState{
		messageID: "msg_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		model:     model,
		toolOpens: map[int]bool{},
	}
}

// anthropicEvent 构造一条 Anthropic SSE 事件（含 event: 行，部分客户端依赖）
func anthropicEvent(event string, payload map[string]any) []byte {
	b, _ := json.Marshal(payload)
	var sb bytes.Buffer
	sb.WriteString("event: ")
	sb.WriteString(event)
	sb.WriteString("\n")
	sb.WriteString("data: ")
	sb.Write(b)
	sb.WriteString("\n\n")
	return sb.Bytes()
}

// convertOpenAIStreamChunkToAnthropic 把一条 OpenAI SSE data 负载转成 Anthropic 事件序列。
// 返回 nil 表示该 chunk 无需输出（例如纯 usage 帧）。
func (st *anthropicStreamState) convertChunk(data []byte) [][]byte {
	var chunk openai.ChatCompletionsStreamResponse
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil
	}
	if chunk.Model != "" {
		st.model = chunk.Model
	}
	if chunk.Usage != nil {
		if chunk.Usage.PromptTokens > 0 {
			st.inputTokens = chunk.Usage.PromptTokens
		}
		if chunk.Usage.CompletionTokens > 0 {
			st.outputTokens = chunk.Usage.CompletionTokens
		}
	}

	out := make([][]byte, 0, 4)

	// 首个 chunk：补 message_start（Anthropic 客户端强依赖这个事件）
	if !st.started {
		st.started = true
		out = append(out, anthropicEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": st.messageID, "type": "message", "role": "assistant",
				"model": st.model, "content": []any{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": st.inputTokens, "output_tokens": 0},
			},
		}))
	}

	for _, choice := range chunk.Choices {
		// 思考增量 -> thinking block（若上游给的是 reasoning_content）
		if s, ok := choice.Delta.ReasoningContent.(string); ok && s != "" {
			if !st.textOpen {
				// 思考块单独占一个 index
				out = append(out, anthropicEvent("content_block_start", map[string]any{
					"type": "content_block_start", "index": st.blockIndex,
					"content_block": map[string]any{"type": "thinking", "thinking": ""},
				}))
				st.textOpen = true
			}
			out = append(out, anthropicEvent("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": st.blockIndex,
				"delta": map[string]any{"type": "thinking_delta", "thinking": s},
			}))
		}

		// 文本增量
		if s, ok := choice.Delta.Content.(string); ok && s != "" {
			if !st.textOpen {
				out = append(out, anthropicEvent("content_block_start", map[string]any{
					"type": "content_block_start", "index": st.blockIndex,
					"content_block": map[string]any{"type": "text", "text": ""},
				}))
				st.textOpen = true
			}
			out = append(out, anthropicEvent("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": st.blockIndex,
				"delta": map[string]any{"type": "text_delta", "text": s},
			}))
		}

		// 工具调用增量
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			if !st.toolOpens[idx] {
				st.toolOpens[idx] = true
				// 工具块之前要先关掉已开的文本块
				if st.textOpen {
					out = append(out, anthropicEvent("content_block_stop", map[string]any{
						"type": "content_block_stop", "index": st.blockIndex,
					}))
					st.textOpen = false
					st.blockIndex++
				}
				id := tc.Id
				if id == "" {
					id = "toolu_" + fmt.Sprintf("%d", time.Now().UnixNano())
				}
				out = append(out, anthropicEvent("content_block_start", map[string]any{
					"type": "content_block_start", "index": st.blockIndex,
					"content_block": map[string]any{
						"type": "tool_use", "id": id, "name": tc.Function.Name, "input": map[string]any{},
					},
				}))
			}
			if s, ok := tc.Function.Arguments.(string); ok && s != "" {
				out = append(out, anthropicEvent("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": st.blockIndex,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": s},
				}))
			}
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			st.stopReason = anthropicStopReasonFromFinishReason(*choice.FinishReason)
			// 关掉打开的内容块
			if st.textOpen {
				out = append(out, anthropicEvent("content_block_stop", map[string]any{
					"type": "content_block_stop", "index": st.blockIndex,
				}))
				st.textOpen = false
			}
			for idx := range st.toolOpens {
				_ = idx
				out = append(out, anthropicEvent("content_block_stop", map[string]any{
					"type": "content_block_stop", "index": st.blockIndex,
				}))
			}
			// 清空，避免重复 stop
			st.toolOpens = map[int]bool{}
		}
	}
	return out
}

// finishAnthropicStream 产生收尾事件（message_delta + message_stop）
func (st *anthropicStreamState) finish() [][]byte {
	out := make([][]byte, 0, 2)
	if !st.started {
		return out
	}
	if st.textOpen {
		out = append(out, anthropicEvent("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": st.blockIndex,
		}))
		st.textOpen = false
	}
	stopReason := st.stopReason
	if stopReason == "" {
		stopReason = "end_turn"
	}
	out = append(out, anthropicEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": st.outputTokens},
	}))
	out = append(out, anthropicEvent("message_stop", map[string]any{"type": "message_stop"}))
	return out
}

// ---- transcodeWriter ----

// transcodeWriter 包裹 gin.ResponseWriter，把 OpenAI 格式的响应字节
// 实时转成入口协议格式。非流式为整包替换，流式为逐事件转换。
type transcodeWriter struct {
	gin.ResponseWriter
	protocol string
	model    string
	stream   bool

	// 非流式：缓存完整 body，CloseNotify 时/写完时一次性替换
	buf bytes.Buffer
	// 流式：SSE 行缓冲
	sseBuf    bytes.Buffer
	anthState *anthropicStreamState
	// 头部是否已改写
	headerFixed bool
	// 防止重复刷出尾部事件
	flushed bool
}

func (w *transcodeWriter) WriteHeader(code int) {
	w.fixHeaders()
	w.ResponseWriter.WriteHeader(code)
}

func (w *transcodeWriter) WriteHeaderNow() {
	w.fixHeaders()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *transcodeWriter) fixHeaders() {
	if w.headerFixed {
		return
	}
	w.headerFixed = true
	if w.protocol == ctxkey.InboundAnthropic {
		w.ResponseWriter.Header().Set("Content-Type", "text/event-stream")
		w.ResponseWriter.Header().Set("Cache-Control", "no-cache")
		w.ResponseWriter.Header().Set("Connection", "keep-alive")
	}
}

// Write 是核心：按流式/非流式分别处理。
func (w *transcodeWriter) Write(p []byte) (int, error) {
	if w.protocol == ctxkey.InboundGeminiInteractions {
		// Interactions 流式为 JSON 增量，转码收益低、风险高；
		// 此处按非流式整包转换处理（stream=true 时客户端自行拼接）。
		w.buf.Write(p)
		return len(p), nil
	}
	if !w.stream {
		// 非流式：先缓存，Flush/结束时整体转换
		w.buf.Write(p)
		return len(p), nil
	}
	// 流式：按 SSE 事件边界切分并转换
	w.sseBuf.Write(p)
	return w.drainSSE(false)
}

// drainSSE 从缓冲区取出完整的 SSE 事件（以空行分隔）并转码写出。
func (w *transcodeWriter) drainSSE(final bool) (int, error) {
	total := 0
	for {
		data := w.sseBuf.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			if !final {
				break
			}
			// 收尾：把残留当作最后一个事件处理
			if len(data) == 0 {
				break
			}
			idx = len(data)
		}
		event := make([]byte, idx)
		copy(event, data[:idx])
		if idx < len(data) {
			w.sseBuf.Next(idx + 2)
		} else {
			w.sseBuf.Next(idx)
		}
		if err := w.writeAnthropicEvent(event); err != nil {
			return total, err
		}
		total++
		if final && w.sseBuf.Len() == 0 {
			break
		}
	}
	return total, nil
}

// writeAnthropicEvent 处理一条 OpenAI SSE 事件，写出对应的 Anthropic 事件。
func (w *transcodeWriter) writeAnthropicEvent(event []byte) error {
	line := strings.TrimSpace(string(event))
	if line == "" {
		return nil
	}
	// 取出 data: 负载
	if !strings.HasPrefix(line, "data:") {
		// 非 data 行（event:/id:/注释）直接丢弃——Anthropic 侧由我们重建
		return nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "[DONE]" {
		if w.flushed {
			return nil
		}
		w.flushed = true
		if w.anthState == nil {
			w.anthState = newAnthropicStreamState(w.model)
		}
		for _, ev := range w.anthState.finish() {
			if _, err := w.ResponseWriter.Write(ev); err != nil {
				return err
			}
		}
		return nil
	}
	if w.anthState == nil {
		w.anthState = newAnthropicStreamState(w.model)
	}
	for _, ev := range w.anthState.convertChunk([]byte(payload)) {
		if _, err := w.ResponseWriter.Write(ev); err != nil {
			return err
		}
	}
	return nil
}

// Flush 在流式场景下先冲掉已积累的完整事件，再委托底层 Flush。
func (w *transcodeWriter) Flush() {
	if w.stream && w.protocol == ctxkey.InboundAnthropic {
		_, _ = w.drainSSE(false)
	}
	w.ResponseWriter.Flush()
}

// CloseNotify 透传（gin 1.10 仍使用该接口）
func (w *transcodeWriter) CloseNotify() <-chan bool {
	return w.ResponseWriter.CloseNotify()
}

func (w *transcodeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.Hijack()
}

func (w *transcodeWriter) Pusher() http.Pusher {
	return w.ResponseWriter.Pusher()
}

// finalize 在请求处理结束时，把缓存的非流式 body 转码后写出。
func (w *transcodeWriter) finalize() {
	if w.protocol == ctxkey.InboundAnthropic && w.stream {
		if !w.flushed {
			w.flushed = true
			if w.anthState == nil {
				w.anthState = newAnthropicStreamState(w.model)
			}
			for _, ev := range w.anthState.finish() {
				_, _ = w.ResponseWriter.Write(ev)
			}
		}
		return
	}
	if w.buf.Len() == 0 {
		return
	}
	raw := w.buf.Bytes()
	w.buf.Reset()
	var out []byte
	var err error
	switch w.protocol {
	case ctxkey.InboundAnthropic:
		out, err = openAIToAnthropicResponse(raw, w.model)
	case ctxkey.InboundGeminiInteractions:
		out, err = openAIToGeminiInteractionResponse(raw, w.model)
	default:
		out = raw
	}
	if err != nil {
		out = raw
	}
	if _, werr := w.ResponseWriter.Write(out); werr != nil {
		return
	}
}

// maybeWrapForInboundProtocol 在入口协议非 OpenAI 时，替换 c.Writer 为转码层。
// 返回一个函数用于在处理结束后收尾（写出缓存内容）。
func maybeWrapForInboundProtocol(c *gin.Context, modelName string, stream bool) func() {
	protocol := c.GetString(ctxkey.InboundProtocol)
	if protocol == "" {
		return func() {}
	}
	orig := c.Writer
	w := &transcodeWriter{
		ResponseWriter: orig,
		protocol:       protocol,
		model:          modelName,
		stream:         stream,
	}
	c.Writer = w
	return w.finalize
}
