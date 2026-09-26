package openai

// dyt-114: OpenAI Responses 出口侧（网关->上游）转换测试。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/relay/model"
)

func TestResponsesEgressBasic(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "gpt-5",
		Messages: []model.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hello"},
		},
	}
	got := ConvertRequestToResponses(req)

	if got.Model != "gpt-5" {
		t.Fatalf("model: %q", got.Model)
	}
	// system 应移到 instructions，而不是留在 input
	if got.Instructions != "be terse" {
		t.Fatalf("instructions: %q", got.Instructions)
	}
	if len(got.Input) != 1 {
		t.Fatalf("input items: %d", len(got.Input))
	}
	if got.Input[0].Role != "user" || got.Input[0].Type != "message" {
		t.Fatalf("input item: %+v", got.Input[0])
	}
	if got.Input[0].Content[0].Type != "input_text" {
		t.Fatalf("content type: %q", got.Input[0].Content[0].Type)
	}
	if got.Input[0].Content[0].Text != "hello" {
		t.Fatalf("text: %q", got.Input[0].Content[0].Text)
	}
}

func TestResponsesEgressMaxTokensFieldName(t *testing.T) {
	// OpenAI chat 用 max_tokens，Responses 用 max_output_tokens
	req := model.GeneralOpenAIRequest{
		Model:     "m",
		MaxTokens: 1234,
		Messages:  []model.Message{{Role: "user", Content: "x"}},
	}
	got := ConvertRequestToResponses(req)
	if got.MaxOutputTokens != 1234 {
		t.Fatalf("max_output_tokens: %d", got.MaxOutputTokens)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), `"max_tokens"`) {
		t.Fatalf("leaked max_tokens field name: %s", b)
	}
}

func TestResponsesEgressToolCallAndOutput(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "m",
		Messages: []model.Message{
			{Role: "user", Content: "weather?"},
			{Role: "assistant", Content: "", ToolCalls: []model.Tool{{
				Id: "call_1", Type: "function",
				Function: model.Function{Name: "get_weather", Arguments: `{"city":"SF"}`},
			}}},
			{Role: "tool", ToolCallId: "call_1", Content: "sunny"},
		},
	}
	got := ConvertRequestToResponses(req)

	var sawCall, sawOutput bool
	for _, it := range got.Input {
		switch it.Type {
		case "function_call":
			sawCall = true
			if it.CallId != "call_1" || it.Name != "get_weather" {
				t.Fatalf("function_call: %+v", it)
			}
			if string(it.Arguments) != `{"city":"SF"}` {
				t.Fatalf("arguments: %s", it.Arguments)
			}
		case "function_call_output":
			sawOutput = true
			if it.CallId != "call_1" || it.Output != "sunny" {
				t.Fatalf("function_call_output: %+v", it)
			}
		}
	}
	if !sawCall {
		t.Fatalf("function_call missing: %+v", got.Input)
	}
	if !sawOutput {
		t.Fatalf("function_call_output missing: %+v", got.Input)
	}
}

func TestResponsesEgressImage(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model: "m",
		Messages: []model.Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/a.png"}},
			},
		}},
	}
	got := ConvertRequestToResponses(req)
	content := got.Input[0].Content
	if len(content) != 2 {
		t.Fatalf("content: %d", len(content))
	}
	if content[1].Type != "input_image" || content[1].ImageURL != "https://x/a.png" {
		t.Fatalf("image content: %+v", content[1])
	}
}

func TestResponsesEgressEmptyInputSafety(t *testing.T) {
	got := ConvertRequestToResponses(model.GeneralOpenAIRequest{Model: "m"})
	if len(got.Input) != 1 {
		t.Fatalf("input: %d", len(got.Input))
	}
}

func TestResponsesEgressSerializable(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model:    "m",
		Messages: []model.Message{{Role: "user", Content: "x"}},
	}
	got := ConvertRequestToResponses(req)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"input"`) {
		t.Fatalf("no input: %s", s)
	}
	if strings.Contains(s, `"messages"`) {
		t.Fatalf("leaked chat shape: %s", s)
	}
}

func TestResponsesEgressResponseMerge(t *testing.T) {
	ir := &responsesResponse{
		Id:    "resp_1",
		Model: "gpt-5",
		Output: []responsesOutputItem{
			{Type: "message", Content: []responsesOutputContent{
				{Type: "output_text", Text: "Hello "},
				{Type: "output_text", Text: "world"},
			}},
		},
		Usage: &responsesUsage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7},
	}
	text, tools, usage := MergeNonStreamResponsesIntoChat(ir)
	if text != "Hello world" {
		t.Fatalf("text: %q", text)
	}
	if len(tools) != 0 {
		t.Fatalf("tools: %d", len(tools))
	}
	if usage.InputTokens != 5 {
		t.Fatalf("usage: %+v", usage)
	}
}

func TestResponsesEgressResponseFunctionCall(t *testing.T) {
	ir := &responsesResponse{
		Id: "resp_1",
		Output: []responsesOutputItem{
			{Type: "function_call", CallId: "call_9", Name: "get_weather", Arguments: `{"city":"SF"}`},
		},
	}
	text, tools, _ := MergeNonStreamResponsesIntoChat(ir)
	if text != "" {
		t.Fatalf("text should be empty: %q", text)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: %d", len(tools))
	}
	if tools[0].Id != "call_9" || tools[0].Function.Name != "get_weather" {
		t.Fatalf("tool: %+v", tools[0])
	}
}

// dyt-115: 推理吃光 max_output_tokens 时，不能返回空 content。
// 真实场景：Muse Spark 1.3 在 max_tokens=300 下可能把额度全用于推理，
// upstream 返回 status=completed 但 output 里只有 reasoning、没有 message。
func TestResponsesReasoningOnlyNotSilent(t *testing.T) {
	ir := &responsesResponse{
		Id:     "resp_1",
		Status: "completed",
		Output: []responsesOutputItem{
			{Type: "reasoning", Id: "rs_1", Status: "completed"},
		},
		Usage: &responsesUsage{InputTokens: 13, OutputTokens: 300, TotalTokens: 313},
	}
	ir.Usage.OutputTokensDetails.ReasoningTokens = 300

	text, tools, _ := MergeNonStreamResponsesIntoChat(ir)
	if len(tools) != 0 {
		t.Fatalf("tools: %d", len(tools))
	}
	if text == "" {
		t.Fatal("must not be silent when reasoning consumed the whole budget")
	}
	if !strings.Contains(text, "max_tokens") {
		t.Fatalf("hint should mention max_tokens: %q", text)
	}
}

// 有推理摘要时优先返回摘要内容
func TestResponsesReasoningSummaryUsed(t *testing.T) {
	ir := &responsesResponse{
		Output: []responsesOutputItem{
			{Type: "reasoning", Summary: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "summary_text", Text: "I reasoned about it."}}},
		},
		Usage: &responsesUsage{OutputTokens: 100},
	}
	text, _, _ := MergeNonStreamResponsesIntoChat(ir)
	if text != "I reasoned about it." {
		t.Fatalf("expected summary text, got %q", text)
	}
}

// 正常有 message 时不应被推理逻辑干扰
func TestResponsesNormalMessageStillWins(t *testing.T) {
	ir := &responsesResponse{
		Output: []responsesOutputItem{
			{Type: "reasoning", Status: "completed"},
			{Type: "message", Content: []responsesOutputContent{{Type: "output_text", Text: "real answer"}}},
		},
		Usage: &responsesUsage{OutputTokens: 50},
	}
	ir.Usage.OutputTokensDetails.ReasoningTokens = 40
	text, _, _ := MergeNonStreamResponsesIntoChat(ir)
	if text != "real answer" {
		t.Fatalf("got %q", text)
	}
}

// 完全没有推理也没有内容时，不应编造提示
func TestResponsesGenuinelyEmptyNotPadded(t *testing.T) {
	ir := &responsesResponse{
		Output: []responsesOutputItem{},
		Usage:  &responsesUsage{OutputTokens: 0},
	}
	text, _, _ := MergeNonStreamResponsesIntoChat(ir)
	if text != "" {
		t.Fatalf("genuinely empty should stay empty, got %q", text)
	}
}
