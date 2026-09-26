package anthropic

// dyt-114: Anthropic 出口侧新特性透传测试。
//
// 背景：原实现把 anthropic-beta 硬编码为 messages-2023-12-15，
// 且 Request 结构里没有 thinking / context_management 字段，
// 导致客户端启用的扩展思考与上下文压缩在上游完全失效（请求仍 200，
// 属于"静默失效"——最难排查的一类问题）。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/relay/model"
)

func TestCollectAnthropicBetasSingle(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	got := collectAnthropicBetas(h)
	if len(got) != 1 || got[0] != "interleaved-thinking-2025-05-14" {
		t.Fatalf("betas: %v", got)
	}
}

func TestCollectAnthropicBetasCommaSeparated(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-beta", "beta-a, beta-b ,beta-c")
	got := collectAnthropicBetas(h)
	if len(got) != 3 {
		t.Fatalf("betas: %v", got)
	}
	for i, want := range []string{"beta-a", "beta-b", "beta-c"} {
		if got[i] != want {
			t.Fatalf("beta[%d]: got %q want %q", i, got[i], want)
		}
	}
}

func TestCollectAnthropicBetasMultipleHeaders(t *testing.T) {
	h := http.Header{}
	h.Add("anthropic-beta", "beta-a")
	h.Add("anthropic-beta", "beta-b")
	got := collectAnthropicBetas(h)
	if len(got) != 2 {
		t.Fatalf("betas: %v", got)
	}
}

func TestCollectAnthropicBetasDedup(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-beta", "beta-a,beta-a")
	got := collectAnthropicBetas(h)
	if len(got) != 1 {
		t.Fatalf("should dedup: %v", got)
	}
}

func TestCollectAnthropicBetasEmpty(t *testing.T) {
	if got := collectAnthropicBetas(http.Header{}); len(got) != 0 {
		t.Fatalf("empty header should yield none: %v", got)
	}
	// 空字符串与仅逗号都不应产生空条目
	h := http.Header{}
	h.Set("anthropic-beta", " , ,")
	if got := collectAnthropicBetas(h); len(got) != 0 {
		t.Fatalf("whitespace should be skipped: %v", got)
	}
}

func TestAppendBetaIfMissing(t *testing.T) {
	list := []string{"a"}
	list = appendBetaIfMissing(list, "b")
	if len(list) != 2 {
		t.Fatalf("append failed: %v", list)
	}
	list = appendBetaIfMissing(list, "A") // 大小写不敏感去重
	if len(list) != 2 {
		t.Fatalf("case-insensitive dedup failed: %v", list)
	}
}

// 关键回归：thinking / context_management 必须出现在发往上游的请求体里
func TestAnthropicRequestCarriesThinking(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model:     "claude-opus-4-6",
		MaxTokens: 4096,
		Messages: []model.Message{
			{Role: "user", Content: "solve this"},
		},
		Thinking:          map[string]any{"type": "enabled", "budget_tokens": float64(10000)},
		ContextManagement: map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}},
	}
	claudeReq := ConvertRequest(req)

	b, err := json.Marshal(claudeReq)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	if !strings.Contains(s, `"thinking"`) {
		t.Fatalf("thinking dropped from upstream request: %s", s)
	}
	if !strings.Contains(s, "budget_tokens") {
		t.Fatalf("thinking body lost: %s", s)
	}
	if !strings.Contains(s, `"context_management"`) {
		t.Fatalf("context_management dropped: %s", s)
	}
	if !strings.Contains(s, "compact_20260112") {
		t.Fatalf("compaction edit lost: %s", s)
	}
}

// 未设置时不应凭空产出字段（避免给不支持的上游发多余字段）
func TestAnthropicRequestOmitsThinkingWhenUnset(t *testing.T) {
	req := model.GeneralOpenAIRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		Messages:  []model.Message{{Role: "user", Content: "hi"}},
	}
	claudeReq := ConvertRequest(req)
	b, _ := json.Marshal(claudeReq)
	if strings.Contains(string(b), `"thinking"`) {
		t.Fatalf("thinking should be omitted when unset: %s", b)
	}
	if strings.Contains(string(b), `"context_management"`) {
		t.Fatalf("context_management should be omitted when unset: %s", b)
	}
}
