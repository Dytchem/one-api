package controller

// dyt-116: 出口 Responses SSE -> 客户端 chat SSE 转码测试。
// 用真实捕获的上游流（Muse Spark 1.3 经 OpenCode Go /v1/responses）驱动。

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

func loadMuseSSE(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("testdata_muse_sse.txt")
	if err != nil {
		t.Skip("no captured SSE fixture")
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// 核心回归：探测必须能从 Responses SSE 里确认到首个 token。
// 修复前探测只认 choices 字段，导致 "all N probe attempts returned empty" → 502。
func TestResponsesSSEProducesChatContentChunk(t *testing.T) {
	st := newResponsesToChatStreamState("muse-spark-1.3-contributor")
	var contentSeen string
	for _, line := range loadMuseSSE(t) {
		chunk := st.feed(line)
		if chunk == nil {
			continue
		}
		var parsed struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(chunk, &parsed); err != nil {
			t.Fatalf("chunk not valid chat JSON: %s", chunk)
		}
		for _, c := range parsed.Choices {
			contentSeen += c.Delta.Content
		}
	}
	if contentSeen == "" {
		t.Fatal("no chat content produced — probe would report empty response (the 502 bug)")
	}
	if !strings.Contains(contentSeen, "PONG") {
		t.Fatalf("expected PONG in transcoded output, got %q", contentSeen)
	}
}

// 必须产出 choices 结构，否则探测的判定逻辑识别不到
func TestResponsesSSEChunkShapeIsChatCompatible(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	line := `data: {"type":"response.output_text.delta","delta":"X"}`
	chunk := st.feed(line)
	if chunk == nil {
		t.Fatal("delta should produce a chunk")
	}
	var parsed map[string]any
	if err := json.Unmarshal(chunk, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if parsed["object"] != "chat.completion.chunk" {
		t.Fatalf("object: %v", parsed["object"])
	}
	choices, ok := parsed["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("choices missing/empty: %s", chunk)
	}
	first := choices[0].(map[string]any)
	delta, ok := first["delta"].(map[string]any)
	if !ok {
		t.Fatalf("delta missing: %s", chunk)
	}
	if delta["content"] != "X" {
		t.Fatalf("content: %v", delta["content"])
	}
}

func TestResponsesSSERoleChunkOnCreated(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	chunk := st.feed(`data: {"type":"response.created","response":{"id":"resp_1","model":"muse-spark-1.3-contributor"}}`)
	if chunk == nil {
		t.Fatal("created should emit role chunk")
	}
	if !strings.Contains(string(chunk), `"role":"assistant"`) {
		t.Fatalf("role chunk missing: %s", chunk)
	}
}

func TestResponsesSSEUsageOnCompleted(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	st.feed(`data: {"type":"response.output_text.delta","delta":"hi"}`)
	chunk := st.feed(`data: {"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":13,"output_tokens":121,"total_tokens":134}}}`)
	if chunk == nil {
		t.Fatal("completed should emit final chunk")
	}
	if !strings.Contains(string(chunk), `"prompt_tokens":13`) {
		t.Fatalf("usage missing: %s", chunk)
	}
	if st.usage == nil || st.usage.TotalTokens != 134 {
		t.Fatalf("usage not captured: %+v", st.usage)
	}
}

// 推理摘要增量应转成 reasoning_content
func TestResponsesSSEReasoningDelta(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	chunk := st.feed(`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking..."}`)
	if chunk == nil {
		t.Fatal("reasoning delta should produce a chunk")
	}
	if !strings.Contains(string(chunk), `"reasoning_content":"thinking..."`) {
		t.Fatalf("reasoning_content missing: %s", chunk)
	}
}

// 无关键事件不应产生输出（避免噪声/重复）
func TestResponsesSSEIgnoresNoiseEvents(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	noise := []string{
		`data: {"type":"response.output_item.added","item":{"type":"reasoning"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"message"}}`,
		`data: {"type":"response.content_part.added","part":{"type":"output_text"}}`,
		`event: response.output_text.delta`,
		`data: [DONE]`,
		``,
		`: keep-alive`,
	}
	for _, l := range noise {
		if chunk := st.feed(l); chunk != nil {
			t.Fatalf("noise line produced output: %q -> %s", l, chunk)
		}
	}
}

func TestResponsesSSEMalformedJSONIgnored(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	if chunk := st.feed(`data: {not json`); chunk != nil {
		t.Fatalf("malformed should be ignored: %s", chunk)
	}
}

func TestResponsesSSEFailedSurfacesError(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	chunk := st.feed(`data: {"type":"response.failed","response":{"error":{"message":"boom"}}}`)
	if chunk == nil {
		t.Fatal("failed should emit something")
	}
	if !strings.Contains(string(chunk), "boom") {
		t.Fatalf("error message lost: %s", chunk)
	}
}

// dyt-116 回归：流式探测不能把 chat 字段直接发给只认 Responses 的上游。
// 实测故障：探测发出 max_tokens，上游回 400 unknown parameter `max_tokens`，
// 探测把它当成"空响应"，最终对客户端报 empty_response（表现为 502）。
func TestProbeBodyUsesMaxOutputTokensForResponsesEgress(t *testing.T) {
	req := relaymodel.GeneralOpenAIRequest{
		Model:     "muse-spark-1.3-contributor",
		MaxTokens: 2000,
		Stream:    true,
		Messages:  []relaymodel.Message{{Role: "user", Content: "hi"}},
	}
	converted := openai.ConvertRequestToResponses(req)
	b, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if strings.Contains(got, `"max_tokens"`) {
		t.Fatalf("must not send max_tokens to Responses upstream: %s", got)
	}
	if !strings.Contains(got, `"max_output_tokens":2000`) {
		t.Fatalf("max_output_tokens missing: %s", got)
	}
	// 也不能带 chat 专有字段
	for _, bad := range []string{`"messages"`, `"stream_options"`} {
		if strings.Contains(got, bad) {
			t.Fatalf("chat-only field %s leaked: %s", bad, got)
		}
	}
}

// dyt-116 回归：转码层绝不能把上游的 `event:` 行当成 data 负载产出。
// 实测畸形输出：`data: event: response.created`（客户端解析失败）。
func TestTranscoderNeverEmitsEventLineAsData(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	// 上游原始流里 event: 与 data: 交替出现，两者都会喂给 feed()
	lines := loadMuseSSE(t)
	for _, line := range lines {
		chunk := st.feed(line)
		if chunk == nil {
			continue
		}
		s := string(chunk)
		if strings.HasPrefix(s, "event:") || strings.Contains(s, "event: response") {
			t.Fatalf("transcoder emitted an SSE event line as payload: %q", s)
		}
		// 产出必须是合法 JSON
		var probe map[string]any
		if err := json.Unmarshal(chunk, &probe); err != nil {
			t.Fatalf("chunk is not valid JSON: %q", s)
		}
	}
}

// event: 行本身必须被忽略（不是 data 负载）
func TestTranscoderIgnoresBareEventLine(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	for _, l := range []string{"event: response.created", "event: response.output_text.delta"} {
		if chunk := st.feed(l); chunk != nil {
			t.Fatalf("bare event line produced output: %q -> %s", l, chunk)
		}
	}
}

// dyt-118: function_call 的名称只在 response.output_item.added 里，
// arguments 增量里没有 name。若丢了这个事件，Agent 拿到的 tool_call 是
// name=""，无法据此分发工具（表现为"模型说要调工具但什么都没发生"）。
func TestResponsesSSEFunctionCallNameFromItemAdded(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	// 上游真实形态：名称在 item 里
	added := `data: {"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"list_channels","call_id":"call_1","arguments":""}}`
	chunk := st.feed(added)
	if chunk == nil {
		t.Fatal("output_item.added for function_call must emit a tool_call chunk")
	}
	s := string(chunk)
	if !strings.Contains(s, `"name":"list_channels"`) {
		t.Fatalf("tool name missing: %s", s)
	}
	if !strings.Contains(s, `"id":"call_1"`) {
		t.Fatalf("tool call id missing: %s", s)
	}

	// arguments 增量只带 arguments，不重复 name
	delta := st.feed(`data: {"type":"response.function_call_arguments.delta","delta":"{\"a\":1}"}`)
	if delta == nil {
		t.Fatal("arguments delta must emit")
	}
	ds := string(delta)
	if strings.Contains(ds, `"name":"list_channels"`) {
		t.Fatalf("name must not repeat in arguments delta (would concatenate): %s", ds)
	}
	if !strings.Contains(ds, `"arguments":"{\"a\":1}"`) {
		t.Fatalf("arguments missing: %s", ds)
	}
}

// 非 function_call 的 output_item.added 不应产出内容
func TestResponsesSSEItemAddedNonFunctionIgnored(t *testing.T) {
	st := newResponsesToChatStreamState("m")
	for _, l := range []string{
		`data: {"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_1"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant"}}`,
	} {
		if c := st.feed(l); c != nil {
			t.Fatalf("non-function item should be ignored: %q -> %s", l, c)
		}
	}
}
