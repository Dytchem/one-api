package gemini

// dyt-114: Gemini Interactions 出口侧（网关->上游）转换测试。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/relay/model"
)

func TestInteractionsEgressBasic(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "gemini-3.6-flash",
		Messages: []model.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hello"},
		},
	}
	got := ConvertRequestToInteractions(req)

	if got.Model != "gemini-3.6-flash" {
		t.Fatalf("model: %q", got.Model)
	}
	if len(got.Input) != 1 {
		t.Fatalf("input items: %d (system should move to system_instruction)", len(got.Input))
	}
	if got.Input[0].Type != "user_input" {
		t.Fatalf("input type: %q", got.Input[0].Type)
	}
	if got.Input[0].Content[0].Text != "hello" {
		t.Fatalf("text: %q", got.Input[0].Content[0].Text)
	}
	if got.SystemInstruction == nil || len(got.SystemInstruction.Parts) != 1 {
		t.Fatalf("system_instruction missing: %+v", got.SystemInstruction)
	}
	if got.SystemInstruction.Parts[0].Text != "be terse" {
		t.Fatalf("system text: %q", got.SystemInstruction.Parts[0].Text)
	}
}

func TestInteractionsEgressRoleMapping(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "gemini-3.6-flash",
		Messages: []model.Message{
			{Role: "user", Content: "q1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "q2"},
		},
	}
	got := ConvertRequestToInteractions(req)
	if len(got.Input) != 3 {
		t.Fatalf("input: %d", len(got.Input))
	}
	want := []string{"user_input", "model_output", "user_input"}
	for i, w := range want {
		if got.Input[i].Type != w {
			t.Fatalf("input[%d] type: got %q want %q", i, got.Input[i].Type, w)
		}
	}
}

func TestInteractionsEgressGenerationConfig(t *testing.T) {
	temp := 0.7
	topP := 0.9
	req := model.GeneralOpenAIRequest{
		Model:       "m",
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   2048,
		Stop:        "STOP",
		Messages:    []model.Message{{Role: "user", Content: "x"}},
	}
	got := ConvertRequestToInteractions(req)
	if got.GenerationConfig == nil {
		t.Fatal("generation_config missing")
	}
	if *got.GenerationConfig.Temperature != 0.7 {
		t.Fatalf("temperature: %v", *got.GenerationConfig.Temperature)
	}
	if *got.GenerationConfig.TopP != 0.9 {
		t.Fatalf("top_p: %v", *got.GenerationConfig.TopP)
	}
	if got.GenerationConfig.MaxOutputTokens != 2048 {
		t.Fatalf("max_output_tokens: %d", got.GenerationConfig.MaxOutputTokens)
	}
	if len(got.GenerationConfig.StopSequences) != 1 || got.GenerationConfig.StopSequences[0] != "STOP" {
		t.Fatalf("stop_sequences: %v", got.GenerationConfig.StopSequences)
	}
}

func TestInteractionsEgressMultiStop(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model:    "m",
		Stop:     []any{"A", "B"},
		Messages: []model.Message{{Role: "user", Content: "x"}},
	}
	got := ConvertRequestToInteractions(req)
	if len(got.GenerationConfig.StopSequences) != 2 {
		t.Fatalf("stop_sequences: %v", got.GenerationConfig.StopSequences)
	}
}

func TestInteractionsEgressStream(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model:    "m",
		Stream:   true,
		Messages: []model.Message{{Role: "user", Content: "x"}},
	}
	got := ConvertRequestToInteractions(req)
	if !got.Stream {
		t.Fatal("stream flag not carried")
	}
}

func TestInteractionsEgressImageDataURL(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "m",
		Messages: []model.Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "what is this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:image/png;base64,AAAA",
				}},
			},
		}},
	}
	got := ConvertRequestToInteractions(req)
	if len(got.Input) != 1 {
		t.Fatalf("input: %d", len(got.Input))
	}
	content := got.Input[0].Content
	if len(content) != 2 {
		t.Fatalf("content blocks: %d", len(content))
	}
	img := content[1]
	if img.Type != "image" {
		t.Fatalf("image type: %q", img.Type)
	}
	if img.Data != "AAAA" || img.MimeType != "image/png" {
		t.Fatalf("image data/mime: %q / %q", img.Data, img.MimeType)
	}
}

func TestInteractionsEgressImageHTTPURL(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "m",
		Messages: []model.Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "https://example.com/a.png",
				}},
			},
		}},
	}
	got := ConvertRequestToInteractions(req)
	img := got.Input[0].Content[0]
	if img.URI != "https://example.com/a.png" {
		t.Fatalf("uri: %q", img.URI)
	}
}

func TestInteractionsEgressToolCalls(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "m",
		Messages: []model.Message{
			{Role: "user", Content: "weather?"},
			{Role: "assistant", Content: "", ToolCalls: []model.Tool{{
				Id: "call_1", Type: "function",
				Function: model.Function{Name: "get_weather", Arguments: `{"city":"SF"}`},
			}}},
		},
	}
	got := ConvertRequestToInteractions(req)
	// 空 content 的 assistant 仍应产出 model_output（因为带 tool_calls）
	var found bool
	for _, it := range got.Input {
		if it.Type != "model_output" {
			continue
		}
		for _, c := range it.Content {
			if c.Type == "function_call" && c.Text == "get_weather" {
				found = true
				if c.Data != `{"city":"SF"}` {
					t.Fatalf("arguments: %q", c.Data)
				}
			}
		}
	}
	if !found {
		t.Fatalf("function_call not emitted: %+v", got.Input)
	}
}

func TestInteractionsEgressEmptyInputSafety(t *testing.T) {
	// 没有任何消息时也要产出合法的 input，避免上游报 input required
	got := ConvertRequestToInteractions(model.GeneralOpenAIRequest{Model: "m"})
	if len(got.Input) != 1 {
		t.Fatalf("input: %d", len(got.Input))
	}
	if got.Input[0].Type != "user_input" {
		t.Fatalf("type: %q", got.Input[0].Type)
	}
}

func TestInteractionsEgressSerializable(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model:    "m",
		Messages: []model.Message{{Role: "user", Content: "x"}},
	}
	got := ConvertRequestToInteractions(req)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// 必须真的序列化成 Interactions 的形状（input 数组，不是 messages）
	s := string(b)
	if !strings.Contains(s, `"input"`) {
		t.Fatalf("no input field: %s", s)
	}
	if strings.Contains(s, `"messages"`) {
		t.Fatalf("leaked chat messages shape: %s", s)
	}
}

func TestInteractionsEgressResponseParsing(t *testing.T) {
	ir := &interactionsResponse{
		Id:     "int_1",
		Model:  "gemini-3.6-flash",
		Status: "completed",
		Output: []interactionsOutput{
			{Type: "text", Text: "Hello "},
			{Type: "text", Text: "world"},
		},
		Usage: &interactionsUsage{InputTokens: 11, OutputTokens: 3, TotalTokens: 14},
	}
	var sb strings.Builder
	for _, o := range ir.Output {
		sb.WriteString(o.Text)
	}
	if sb.String() != "Hello world" {
		t.Fatalf("text merge: %q", sb.String())
	}
	if ir.Usage.InputTokens != 11 {
		t.Fatalf("usage: %+v", ir.Usage)
	}
}

func TestJSONArgumentsStringify(t *testing.T) {
	if got := stringifyArguments(`{"a":1}`); got != `{"a":1}` {
		t.Fatalf("string args: %q", got)
	}
	if got := stringifyArguments(nil); got != "{}" {
		t.Fatalf("nil args: %q", got)
	}
	if got := stringifyArguments(map[string]any{"a": 1}); got != `{"a":1}` {
		t.Fatalf("object args: %q", got)
	}
}
