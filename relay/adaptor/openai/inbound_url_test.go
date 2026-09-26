package openai

// dyt-113: 原生入口（Anthropic Messages / Gemini Interactions）的上游 URL 改写回归测试。
//
// 背景：meta.RequestURLPath 记录的是"客户端请求的路径"。原生入口的路径是
// /v1/messages 或 /v1beta/interactions，而上游要的是 /v1/chat/completions。
// 端到端实测发现不改写时上游会收到原生路径（404 或语义错位），
// 这里锁住改写行为，避免以后回归。

import (
	"testing"

	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

func TestGetRequestURLRewritesAnthropicIngress(t *testing.T) {
	a := &Adaptor{}
	m := &meta.Meta{
		Mode:           relaymode.AnthropicMessages,
		ChannelType:    channeltype.OpenAI,
		BaseURL:        "https://upstream.example.com",
		RequestURLPath: "/v1/messages",
	}
	got, err := a.GetRequestURL(m)
	if err != nil {
		t.Fatalf("GetRequestURL: %v", err)
	}
	want := "https://upstream.example.com/v1/chat/completions"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGetRequestURLRewritesGeminiInteractionsIngress(t *testing.T) {
	a := &Adaptor{}
	for _, path := range []string{"/v1beta/interactions", "/v1/interactions"} {
		m := &meta.Meta{
			Mode:           relaymode.GeminiInteractions,
			ChannelType:    channeltype.OpenAI,
			BaseURL:        "https://upstream.example.com",
			RequestURLPath: path,
		}
		got, err := a.GetRequestURL(m)
		if err != nil {
			t.Fatalf("GetRequestURL(%s): %v", path, err)
		}
		want := "https://upstream.example.com/v1/chat/completions"
		if got != want {
			t.Fatalf("path %s: got %q, want %q", path, got, want)
		}
	}
}

// 既有行为不能被打断
func TestGetRequestURLNormalChatUnchanged(t *testing.T) {
	a := &Adaptor{}
	m := &meta.Meta{
		Mode:           relaymode.ChatCompletions,
		ChannelType:    channeltype.OpenAI,
		BaseURL:        "https://upstream.example.com",
		RequestURLPath: "/v1/chat/completions",
	}
	got, _ := a.GetRequestURL(m)
	if got != "https://upstream.example.com/v1/chat/completions" {
		t.Fatalf("normal chat path changed: %q", got)
	}
}

func TestGetRequestURLResponsesStillRewritten(t *testing.T) {
	// dyt-53 的既有改写不能被本次改动弄坏
	a := &Adaptor{}
	m := &meta.Meta{
		Mode:           relaymode.Responses,
		ChannelType:    channeltype.OpenAI,
		BaseURL:        "https://upstream.example.com",
		RequestURLPath: "/v1/responses",
	}
	got, _ := a.GetRequestURL(m)
	if got != "https://upstream.example.com/v1/chat/completions" {
		t.Fatalf("responses rewrite broken: %q", got)
	}
}

// base URL 以 /v1 结尾的渠道：改写后不应出现 /v1/v1
func TestGetRequestURLIngressWithVersionedBase(t *testing.T) {
	a := &Adaptor{}
	m := &meta.Meta{
		Mode:           relaymode.AnthropicMessages,
		ChannelType:    channeltype.OpenAICompatible,
		BaseURL:        "https://upstream.example.com/v1",
		RequestURLPath: "/v1/messages",
	}
	got, _ := a.GetRequestURL(m)
	if got == "https://upstream.example.com/v1/v1/chat/completions" {
		t.Fatalf("double version segment: %q", got)
	}
	if got != "https://upstream.example.com/v1/chat/completions" {
		t.Fatalf("unexpected: %q", got)
	}
}
