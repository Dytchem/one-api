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
