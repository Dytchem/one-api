package controller

// dyt-113: Gemini Interactions 原生入口转换测试。

import (
	"encoding/json"
	"strings"
	"testing"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

func TestGeminiInteractionsStringInput(t *testing.T) {
	req := &GeminiInteractionsRequest{
		Model: "gemini-3.6-flash",
		Input: json.RawMessage(`"tell me a story"`),
	}
	got, err := geminiInteractionsToChatRequest(req, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.Model != "gemini-3.6-flash" {
		t.Fatalf("model: %q", got.Model)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages: %d", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content != "tell me a story" {
		t.Fatalf("message: %+v", got.Messages[0])
	}
}

func TestGeminiInteractionsTypedInput(t *testing.T) {
	// 官方文档的 user_input / model_output 形态
	input := `[
		{"type":"user_input","content":[{"type":"text","text":"Hello!"}]},
		{"type":"model_output","content":[{"type":"text","text":"Hi there!"}]},
		{"type":"user_input","content":[{"type":"text","text":"Capital of France?"}]}
	]`
	req := &GeminiInteractionsRequest{
		Model: "gemini-3.6-flash",
		Input: json.RawMessage(input),
	}
	got, err := geminiInteractionsToChatRequest(req, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages: %d", len(got.Messages))
	}
	wantRoles := []string{"user", "assistant", "user"}
	for i, want := range wantRoles {
		if got.Messages[i].Role != want {
			t.Fatalf("message %d role: got %q want %q", i, got.Messages[i].Role, want)
		}
	}
	if got.Messages[1].Content != "Hi there!" {
		t.Fatalf("assistant content: %v", got.Messages[1].Content)
	}
}

func TestGeminiInteractionsFlatBlocks(t *testing.T) {
	// 扁平形态：数组元素本身是 text/image block
	input := `[{"type":"text","text":"What is in this picture?"},{"type":"image","data":"AAAA","mime_type":"image/png"}]`
	req := &GeminiInteractionsRequest{Model: "gemini-3.6-flash", Input: json.RawMessage(input)}
	got, err := geminiInteractionsToChatRequest(req, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages: %d", len(got.Messages))
	}
	parts, ok := got.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("content should be []any (has image), got %T", got.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts: %d", len(parts))
	}
	img := parts[1].(map[string]any)
	iu := img["image_url"].(map[string]any)
	if iu["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("image url: %v", iu["url"])
	}
}

func TestGeminiInteractionsAgentAsModel(t *testing.T) {
	// agent 字段可作为 model 的替代
	req := &GeminiInteractionsRequest{Agent: "deep-research-preview-04-2026", Input: json.RawMessage(`"hi"`)}
	got, err := geminiInteractionsToChatRequest(req, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.Model != "deep-research-preview-04-2026" {
		t.Fatalf("model from agent: %q", got.Model)
	}
}

func TestGeminiInteractionsRejectsServerSideTools(t *testing.T) {
	// 网关无法代持 google_search / mcp_server，必须显式拒绝而不是静默降级
	req := &GeminiInteractionsRequest{
		Model: "gemini-3.6-flash",
		Input: json.RawMessage(`"who is the president of France?"`),
		Tools: []any{map[string]any{"type": "google_search"}},
	}
	_, err := geminiInteractionsToChatRequest(req, nil)
	if err == nil {
		t.Fatal("server-side tools should be rejected")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error should say not supported: %v", err)
	}
}

func TestGeminiInteractionsRejectsBackground(t *testing.T) {
	req := &GeminiInteractionsRequest{
		Model:      "gemini-3.6-flash",
		Input:      json.RawMessage(`"x"`),
		Background: true,
	}
	if _, err := geminiInteractionsToChatRequest(req, nil); err == nil {
		t.Fatal("background should be rejected")
	}
}

func TestGeminiInteractionsRejectsEnvironment(t *testing.T) {
	req := &GeminiInteractionsRequest{
		Model:       "gemini-3.6-flash",
		Input:       json.RawMessage(`"x"`),
		Environment: json.RawMessage(`{"type":"remote"}`),
	}
	if _, err := geminiInteractionsToChatRequest(req, nil); err == nil {
		t.Fatal("remote environment should be rejected")
	}
}

func TestGeminiInteractionsEmptyInputRejected(t *testing.T) {
	req := &GeminiInteractionsRequest{Model: "gemini-3.6-flash"}
	if _, err := geminiInteractionsToChatRequest(req, nil); err == nil {
		t.Fatal("missing input should error")
	}
}

func TestGeminiInteractionsModelRequired(t *testing.T) {
	req := &GeminiInteractionsRequest{Input: json.RawMessage(`"x"`)}
	if _, err := geminiInteractionsToChatRequest(req, nil); err == nil {
		t.Fatal("missing model and agent should error")
	}
}

func TestGeminiSystemInstructionForms(t *testing.T) {
	// 字符串形态
	if got := geminiSystemInstructionToText(json.RawMessage(`"be brief"`)); got != "be brief" {
		t.Fatalf("string system: %q", got)
	}
	// parts 形态
	if got := geminiSystemInstructionToText(json.RawMessage(`{"parts":[{"text":"a"},{"text":"b"}]}`)); got != "a\nb" {
		t.Fatalf("parts system: %q", got)
	}
	if got := geminiSystemInstructionToText(nil); got != "" {
		t.Fatalf("nil system: %q", got)
	}
}

func TestGeminiSystemInstructionPrepend(t *testing.T) {
	req := &GeminiInteractionsRequest{
		Model:             "gemini-3.6-flash",
		Input:             json.RawMessage(`"hi"`),
		SystemInstruction: json.RawMessage(`"be terse"`),
	}
	got, err := geminiInteractionsToChatRequest(req, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages: %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be terse" {
		t.Fatalf("system message: %+v", got.Messages[0])
	}
}

func TestGeminiMaxOutputTokensMapping(t *testing.T) {
	req := &GeminiInteractionsRequest{
		Model:           "gemini-3.6-flash",
		Input:           json.RawMessage(`"x"`),
		MaxOutputTokens: 2048,
	}
	got, _ := geminiInteractionsToChatRequest(req, nil)
	if got.MaxTokens != 2048 {
		t.Fatalf("max_tokens: %d", got.MaxTokens)
	}
}

func TestGeminiHistoryPrepend(t *testing.T) {
	req := &GeminiInteractionsRequest{Model: "gemini-3.6-flash", Input: json.RawMessage(`"follow up"`)}
	history := []struct {
		Role    string
		Content string
	}{{"user", "first"}, {"assistant", "answer"}}

	// 用真实的 relaymodel.Message 类型
	hm := make([]relaymodel.Message, 0, len(history))
	for _, h := range history {
		hm = append(hm, relaymodel.Message{Role: h.Role, Content: h.Content})
	}
	got, err := geminiInteractionsToChatRequest(req, hm)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages: %d", len(got.Messages))
	}
	if got.Messages[2].Content != "follow up" {
		t.Fatalf("last message: %v", got.Messages[2].Content)
	}
}

// ---- 路径解析 ----

func TestRelayModeAnthropicMessages(t *testing.T) {
	if got := relaymode.GetByPath("/v1/messages"); got != relaymode.AnthropicMessages {
		t.Fatalf("/v1/messages -> %d, want AnthropicMessages(%d)", got, relaymode.AnthropicMessages)
	}
}

func TestRelayModeGeminiInteractions(t *testing.T) {
	if got := relaymode.GetByPath("/v1beta/interactions"); got != relaymode.GeminiInteractions {
		t.Fatalf("/v1beta/interactions -> %d, want GeminiInteractions(%d)", got, relaymode.GeminiInteractions)
	}
}

func TestRelayModeOpenAIPathsUnchanged(t *testing.T) {
	// 确认新增分支没有抢走既有路径
	cases := map[string]int{
		"/v1/chat/completions": relaymode.ChatCompletions,
		"/v1/responses":        relaymode.Responses,
		"/v1/completions":      relaymode.Completions,
		"/v1/embeddings":       relaymode.Embeddings,
		"/v1/moderations":      relaymode.Moderations,
	}
	for path, want := range cases {
		if got := relaymode.GetByPath(path); got != want {
			t.Fatalf("%s -> %d, want %d", path, got, want)
		}
	}
}

// ---- 响应侧 ----

func TestOpenAIToGeminiInteractionResponse(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-z","object":"chat.completion","model":"gemini-3.6-flash",
		"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
	}`)
	out, err := openAIToGeminiInteractionResponse(body, "fallback")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["status"] != "completed" {
		t.Fatalf("status: %v", got["status"])
	}
	if got["model"] != "gemini-3.6-flash" {
		t.Fatalf("model: %v", got["model"])
	}
	output := got["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output: %d", len(output))
	}
	blk := output[0].(map[string]any)
	if blk["type"] != "text" || blk["text"] != "pong" {
		t.Fatalf("output block: %+v", blk)
	}
}
