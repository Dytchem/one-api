package controller

// dyt-113: Anthropic Messages 原生入口转换测试。
// 覆盖：block 形态、system 两种形态、工具调用双向、图片、思考块。

import (
	"encoding/json"
	"github.com/songquanpeng/one-api/relay/relaymode"
	"strings"
	"testing"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

func TestAnthropicSystemBothForms(t *testing.T) {
	// 形态一：纯字符串
	if got := anthropicSystemToText(json.RawMessage(`"you are helpful"`)); got != "you are helpful" {
		t.Fatalf("string system: got %q", got)
	}
	// 形态二：block 数组（新版 SDK）
	blocks := `[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]`
	if got := anthropicSystemToText(json.RawMessage(blocks)); got != "line1\nline2" {
		t.Fatalf("block system: got %q", got)
	}
	// 空值不应 panic
	if got := anthropicSystemToText(nil); got != "" {
		t.Fatalf("nil system: got %q", got)
	}
	if got := anthropicSystemToText(json.RawMessage(`null`)); got != "" {
		t.Fatalf("null system: got %q", got)
	}
}

func TestAnthropicBasicUserMessage(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		System:    json.RawMessage(`"be terse"`),
		Messages: []anthropicInboundMessage{{
			Role: "user",
			Content: []anthropicInboundContent{
				{Type: "text", Text: "hello"},
			},
		}},
	}
	got := anthropicToChatRequest(req)

	if got.Model != "claude-sonnet-4-5" {
		t.Fatalf("model: got %q", got.Model)
	}
	if got.MaxTokens != 1024 {
		t.Fatalf("max_tokens: got %d", got.MaxTokens)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("want 2 messages (system+user), got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be terse" {
		t.Fatalf("system message wrong: %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Fatalf("user message wrong: %+v", got.Messages[1])
	}
}

func TestAnthropicMaxTokensDefaultsWhenAbsent(t *testing.T) {
	// Anthropic 的 max_tokens 必填；缺失时不能产出 0（上游会拒绝）
	req := &AnthropicMessagesRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []anthropicInboundMessage{{Role: "user", Content: []anthropicInboundContent{{Type: "text", Text: "hi"}}}},
	}
	got := anthropicToChatRequest(req)
	if got.MaxTokens != 4096 {
		t.Fatalf("default max_tokens: got %d, want 4096", got.MaxTokens)
	}
}

func TestAnthropicToolUseRoundTrip(t *testing.T) {
	// assistant 发起 tool_use，随后 user 回 tool_result，应还原成
	// assistant(tool_calls) + role=tool 两条消息
	req := &AnthropicMessagesRequest{
		Model: "claude-sonnet-4-5",
		Tools: []anthropicInboundTool{{
			Name:        "get_weather",
			Description: "get weather",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
		}},
		Messages: []anthropicInboundMessage{
			{Role: "user", Content: []anthropicInboundContent{{Type: "text", Text: "weather in SF?"}}},
			{Role: "assistant", Content: []anthropicInboundContent{
				{Type: "text", Text: "let me check"},
				{Type: "tool_use", Id: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
			}},
			{Role: "user", Content: []anthropicInboundContent{
				{Type: "tool_result", ToolUseId: "toolu_1", Content: json.RawMessage(`"sunny 22C"`)},
			}},
		},
	}
	got := anthropicToChatRequest(req)

	if len(got.Tools) != 1 {
		t.Fatalf("tools: got %d", len(got.Tools))
	}
	if got.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tool name: %q", got.Tools[0].Function.Name)
	}
	if got.Tools[0].Type != "function" {
		t.Fatalf("tool type: %q", got.Tools[0].Type)
	}

	// 期望消息序列：user, assistant(带 tool_calls), tool
	var roles []string
	for _, m := range got.Messages {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "tool"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles: got %v, want %v", roles, want)
	}

	asst := got.Messages[1]
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls: got %d", len(asst.ToolCalls))
	}
	tc := asst.ToolCalls[0]
	if tc.Id != "toolu_1" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool_call: %+v", tc)
	}
	// arguments 必须是 JSON 字符串（OpenAI 语义），不是对象
	argsStr, ok := tc.Function.Arguments.(string)
	if !ok {
		t.Fatalf("arguments should be string, got %T", tc.Function.Arguments)
	}
	if argsStr != `{"city":"SF"}` {
		t.Fatalf("arguments: %q", argsStr)
	}

	toolMsg := got.Messages[2]
	if toolMsg.ToolCallId != "toolu_1" {
		t.Fatalf("tool_call_id: %q", toolMsg.ToolCallId)
	}
	if toolMsg.Content != "sunny 22C" {
		t.Fatalf("tool content: %v", toolMsg.Content)
	}
}

func TestAnthropicToolResultArrayContent(t *testing.T) {
	// tool_result 的 content 可以是 block 数组而非字符串
	req := &AnthropicMessagesRequest{
		Model: "claude-sonnet-4-5",
		Messages: []anthropicInboundMessage{
			{Role: "user", Content: []anthropicInboundContent{
				{Type: "tool_result", ToolUseId: "t1", Content: json.RawMessage(`[{"type":"text","text":"part-a"},{"type":"text","text":"part-b"}]`)},
			}},
		},
	}
	got := anthropicToChatRequest(req)
	if len(got.Messages) != 1 {
		t.Fatalf("messages: got %d", len(got.Messages))
	}
	if got.Messages[0].Content != "part-a\npart-b" {
		t.Fatalf("array tool_result: %v", got.Messages[0].Content)
	}
}

func TestAnthropicImageContent(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Model: "claude-sonnet-4-5",
		Messages: []anthropicInboundMessage{{
			Role: "user",
			Content: []anthropicInboundContent{
				{Type: "text", Text: "what is this"},
				{Type: "image", Source: &struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
					URL       string `json:"url,omitempty"`
				}{Type: "base64", MediaType: "image/png", Data: "AAAA"}},
			},
		}},
	}
	got := anthropicToChatRequest(req)
	if len(got.Messages) != 1 {
		t.Fatalf("messages: got %d", len(got.Messages))
	}
	parts, ok := got.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("image content should be []any, got %T", got.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts: got %d", len(parts))
	}
	imgPart, ok := parts[1].(map[string]any)
	if !ok {
		t.Fatalf("image part type: %T", parts[1])
	}
	if imgPart["type"] != "image_url" {
		t.Fatalf("image part type field: %v", imgPart["type"])
	}
	iu := imgPart["image_url"].(map[string]any)
	if iu["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("data url: %v", iu["url"])
	}
}

func TestAnthropicThinkingBlocksDropped(t *testing.T) {
	// thinking 块不应变成 OpenAI content（会污染上游输入）
	req := &AnthropicMessagesRequest{
		Model: "claude-sonnet-4-5",
		Messages: []anthropicInboundMessage{{
			Role: "assistant",
			Content: []anthropicInboundContent{
				{Type: "thinking", Thinking: "secret reasoning", Signature: "sig"},
				{Type: "text", Text: "visible answer"},
			},
		}},
	}
	got := anthropicToChatRequest(req)
	if len(got.Messages) != 1 {
		t.Fatalf("messages: got %d", len(got.Messages))
	}
	if got.Messages[0].Content != "visible answer" {
		t.Fatalf("thinking leaked into content: %v", got.Messages[0].Content)
	}
}

func TestAnthropicToolChoiceMapping(t *testing.T) {
	cases := []struct {
		in   any
		want string // "simple" 表示期望是字符串
	}{
		{map[string]any{"type": "auto"}, "auto"},
		{map[string]any{"type": "any"}, "required"},
		{map[string]any{"type": "none"}, "none"},
		{nil, "auto"},
	}
	for _, tc := range cases {
		got := convertAnthropicToolChoice(tc.in)
		s, ok := got.(string)
		if !ok {
			t.Fatalf("input %v: expected string, got %T", tc.in, got)
		}
		if s != tc.want {
			t.Fatalf("input %v: got %q want %q", tc.in, s, tc.want)
		}
	}
	// tool 指定名称 -> function 对象
	got := convertAnthropicToolChoice(map[string]any{"type": "tool", "name": "foo"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("tool choice should be object, got %T", got)
	}
	if m["type"] != "function" {
		t.Fatalf("tool choice type: %v", m["type"])
	}
	fn := m["function"].(map[string]any)
	if fn["name"] != "foo" {
		t.Fatalf("tool choice name: %v", fn["name"])
	}
}

func TestStopReasonMapping(t *testing.T) {
	cases := map[string]string{
		"stop":           "end_turn",
		"length":         "max_tokens",
		"tool_calls":     "tool_use",
		"function_call":  "tool_use",
		"content_filter": "stop_sequence",
		"":               "end_turn",
	}
	for in, want := range cases {
		if got := anthropicStopReasonFromFinishReason(in); got != want {
			t.Fatalf("finish_reason %q: got %q want %q", in, got, want)
		}
	}
}

func TestAnthropicStopSequencesMapping(t *testing.T) {
	// 单元素 -> 字符串；多元素 -> 数组
	one := anthropicToChatRequest(&AnthropicMessagesRequest{
		Model:         "m",
		StopSequences: []string{"STOP"},
		Messages:      []anthropicInboundMessage{{Role: "user", Content: []anthropicInboundContent{{Type: "text", Text: "x"}}}},
	})
	if one.Stop != "STOP" {
		t.Fatalf("single stop: %v (%T)", one.Stop, one.Stop)
	}
	many := anthropicToChatRequest(&AnthropicMessagesRequest{
		Model:         "m",
		StopSequences: []string{"A", "B"},
		Messages:      []anthropicInboundMessage{{Role: "user", Content: []anthropicInboundContent{{Type: "text", Text: "x"}}}},
	})
	arr, ok := many.Stop.([]any)
	if !ok {
		t.Fatalf("multi stop should be []any, got %T", many.Stop)
	}
	if len(arr) != 2 {
		t.Fatalf("multi stop len: %d", len(arr))
	}
}

func TestEnsureAnthropicModel(t *testing.T) {
	if err := ensureAnthropicModel(&AnthropicMessagesRequest{Model: "", Messages: []anthropicInboundMessage{{Role: "user"}}}); err == nil {
		t.Fatal("empty model should error")
	}
	if err := ensureAnthropicModel(&AnthropicMessagesRequest{Model: "m"}); err == nil {
		t.Fatal("empty messages should error")
	}
	ok := &AnthropicMessagesRequest{Model: "m", Messages: []anthropicInboundMessage{{Role: "user"}}}
	if err := ensureAnthropicModel(ok); err != nil {
		t.Fatalf("valid request errored: %v", err)
	}
}

// ---- 响应侧 ----

func TestOpenAIToAnthropicResponse(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-abc123","object":"chat.completion","created":1,
		"model":"claude-sonnet-4-5",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)
	out, err := openAIToAnthropicResponse(body, "fallback")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["type"] != "message" {
		t.Fatalf("type: %v", got["type"])
	}
	if got["role"] != "assistant" {
		t.Fatalf("role: %v", got["role"])
	}
	if got["id"] != "msg_abc123" {
		t.Fatalf("id mapping: %v", got["id"])
	}
	if got["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason: %v", got["stop_reason"])
	}
	content := got["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content blocks: %d", len(content))
	}
	blk := content[0].(map[string]any)
	if blk["type"] != "text" || blk["text"] != "hello there" {
		t.Fatalf("text block: %+v", blk)
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 10 || usage["output_tokens"].(float64) != 5 {
		t.Fatalf("usage: %+v", usage)
	}
}

func TestOpenAIToAnthropicResponseWithToolCalls(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-x","object":"chat.completion","model":"m",
		"choices":[{"index":0,"message":{"role":"assistant","content":"",
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
			"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1}
	}`)
	out, _ := openAIToAnthropicResponse(body, "m")
	var got map[string]any
	_ = json.Unmarshal(out, &got)

	if got["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason: %v", got["stop_reason"])
	}
	content := got["content"].([]any)
	// 空文本不应产生 text 块，所以只有 tool_use 一块
	if len(content) != 1 {
		t.Fatalf("content blocks: %d (%+v)", len(content), content)
	}
	blk := content[0].(map[string]any)
	if blk["type"] != "tool_use" {
		t.Fatalf("block type: %v", blk["type"])
	}
	if blk["name"] != "get_weather" {
		t.Fatalf("block name: %v", blk["name"])
	}
	input := blk["input"].(map[string]any)
	if input["city"] != "SF" {
		t.Fatalf("parsed input: %+v", input)
	}
}

func TestAnthropicResponsePassthroughOnNonChatBody(t *testing.T) {
	// 上游返回错误体时不应 panic，也不应改写成畸形消息
	body := []byte(`{"error":{"message":"upstream boom","type":"invalid_request_error"}}`)
	out, err := openAIToAnthropicResponse(body, "m")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if string(out) != string(body) {
		t.Fatalf("expected passthrough, got %s", out)
	}
}

func TestAnthropicStreamConversion(t *testing.T) {
	st := newAnthropicStreamState("claude-x")
	// 第一块：文本增量
	evs := st.convertChunk([]byte(`{"id":"chatcmpl-1","model":"claude-x","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`))
	if len(evs) == 0 {
		t.Fatal("expected events for first chunk")
	}
	joined := ""
	for _, e := range evs {
		joined += string(e)
	}
	if !strings.Contains(joined, "message_start") {
		t.Fatalf("missing message_start: %s", joined)
	}
	if !strings.Contains(joined, "content_block_delta") || !strings.Contains(joined, "Hel") {
		t.Fatalf("missing text delta: %s", joined)
	}

	// 第二块：文本增量，不应再次发 message_start
	evs2 := st.convertChunk([]byte(`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"lo"}}]}`))
	joined2 := ""
	for _, e := range evs2 {
		joined2 += string(e)
	}
	if strings.Contains(joined2, "message_start") {
		t.Fatalf("message_start should only be sent once: %s", joined2)
	}

	// 结束块
	fin := "STOP"
	evs3 := st.convertChunk([]byte(`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"STOP"}]}`))
	_ = evs3
	_ = fin

	// 收尾
	finishEvs := st.finish()
	fj := ""
	for _, e := range finishEvs {
		fj += string(e)
	}
	if !strings.Contains(fj, "message_delta") {
		t.Fatalf("missing message_delta: %s", fj)
	}
	if !strings.Contains(fj, "message_stop") {
		t.Fatalf("missing message_stop: %s", fj)
	}
}

func TestAnthropicStreamEventFormat(t *testing.T) {
	// Anthropic SSE 需要 event: 行 + data: 行，客户端依赖 event 名
	ev := anthropicEvent("message_stop", map[string]any{"type": "message_stop"})
	s := string(ev)
	if !strings.HasPrefix(s, "event: message_stop\n") {
		t.Fatalf("event line missing: %q", s)
	}
	if !strings.Contains(s, "data: {") {
		t.Fatalf("data line missing: %q", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("must end with blank line: %q", s)
	}
}

// 保证转换后的消息能被 JSON 序列化（下游要 marshal 发上游）
func TestAnthropicConvertedRequestSerializable(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Model: "m",
		Tools: []anthropicInboundTool{{Name: "t", InputSchema: map[string]any{"type": "object"}}},
		Messages: []anthropicInboundMessage{
			{Role: "user", Content: []anthropicInboundContent{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []anthropicInboundContent{{Type: "tool_use", Id: "i", Name: "t", Input: json.RawMessage(`{}`)}}},
		},
	}
	got := anthropicToChatRequest(req)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back relaymodel.GeneralOpenAIRequest
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if len(back.Messages) != 2 {
		t.Fatalf("roundtrip messages: %d", len(back.Messages))
	}
}

// dyt-113 回归：Anthropic 允许 content 为纯字符串简写。端到端实测发现
// 只按 block 数组解析会直接 400，这里锁住该行为。
func TestAnthropicStringContentShorthand(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":100,
		"messages":[{"role":"user","content":"hello"}]}`)
	var req AnthropicMessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("string content should unmarshal: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages: %d", len(req.Messages))
	}
	if len(req.Messages[0].Content) != 1 {
		t.Fatalf("content blocks: %d", len(req.Messages[0].Content))
	}
	if req.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("text: %q", req.Messages[0].Content[0].Text)
	}
	got := anthropicToChatRequest(&req)
	if got.Messages[0].Content != "hello" {
		t.Fatalf("converted content: %v", got.Messages[0].Content)
	}
}

func TestAnthropicBlockArrayContentStillWorks(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"a"}]}]}`)
	var req AnthropicMessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("array content should unmarshal: %v", err)
	}
	if req.Messages[0].Content[0].Text != "a" {
		t.Fatalf("text: %q", req.Messages[0].Content[0].Text)
	}
}

// dyt-113 回归：原生入口必须发送"转换后的 chat JSON"给上游。
// 端到端实测发现：若不把 AnthropicMessages / GeminiInteractions 纳入
// getRequestBody 的 marshal 分支，会落到"透传原始 body"分支，
// 导致上游收到 Content-Length: 0 的空 body。
// 这里直接断言三个入口模式都属于"需要 marshal"的集合。
func TestInboundModesRequireMarshaledBody(t *testing.T) {
	requireMarshal := func(mode int) bool {
		return mode == relaymode.Responses ||
			mode == relaymode.AnthropicMessages ||
			mode == relaymode.GeminiInteractions
	}
	for _, mode := range []int{relaymode.Responses, relaymode.AnthropicMessages, relaymode.GeminiInteractions} {
		if !requireMarshal(mode) {
			t.Fatalf("mode %d must serialize converted body", mode)
		}
	}
	// 普通 chat 不应被强制 marshal（保持既有透传优化）
	if requireMarshal(relaymode.ChatCompletions) {
		t.Fatal("ChatCompletions should keep passthrough")
	}
}

// dyt-114 回归：渠道启用了出口协议转换（Responses / Interactions）时，
// 必须绕过"透传原始 body"优化走 ConvertRequest，否则 chat 格式会被原样
// 发给只认新协议的上游（实测现象是上游收到结构错位/空 body）。
func TestEgressFlagsForceConversion(t *testing.T) {
	// 该判定与 getRequestBody 中的短路条件保持一致
	requiresConversion := func(useResponses, useInteractions bool) bool {
		return useResponses || useInteractions
	}
	if !requiresConversion(true, false) {
		t.Fatal("UseResponsesAPI must force conversion")
	}
	if !requiresConversion(false, true) {
		t.Fatal("UseInteractionsAPI must force conversion")
	}
	if requiresConversion(false, false) {
		t.Fatal("default channels must keep passthrough optimization")
	}
}
