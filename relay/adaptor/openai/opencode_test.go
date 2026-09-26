package openai

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
)

// dyt-104: OpenCode Zen / Go 请求头自动补充的回归测试
func TestIsOpenCodeChannel(t *testing.T) {
	Convey("识别 OpenCode 渠道", t, func() {
		Convey("按渠道类型识别", func() {
			So(IsOpenCodeChannel(&meta.Meta{ChannelType: channeltype.OpenCodeGo}), ShouldBeTrue)
			So(IsOpenCodeChannel(&meta.Meta{ChannelType: channeltype.OpenCodeZen}), ShouldBeTrue)
		})

		Convey("按 base_url 识别（OpenAICompatible 直填域名）", func() {
			So(IsOpenCodeChannel(&meta.Meta{
				ChannelType: channeltype.OpenAICompatible,
				BaseURL:     "https://opencode.ai/zen/go/v1",
			}), ShouldBeTrue)
			So(IsOpenCodeChannel(&meta.Meta{
				ChannelType: channeltype.OpenAICompatible,
				BaseURL:     "opencode.ai/zen/v1",
			}), ShouldBeTrue)
			So(IsOpenCodeChannel(&meta.Meta{
				ChannelType: channeltype.OpenAICompatible,
				BaseURL:     "https://api.opencode.ai",
			}), ShouldBeTrue)
		})

		Convey("不误判其他渠道", func() {
			So(IsOpenCodeChannel(&meta.Meta{ChannelType: channeltype.OpenAI}), ShouldBeFalse)
			So(IsOpenCodeChannel(&meta.Meta{
				ChannelType: channeltype.OpenAICompatible,
				BaseURL:     "https://api.deepseek.com",
			}), ShouldBeFalse)
			// 形似但非 opencode.ai（防后缀误判）
			So(IsOpenCodeChannel(&meta.Meta{
				ChannelType: channeltype.OpenAICompatible,
				BaseURL:     "https://notopencode.ai/v1",
			}), ShouldBeFalse)
			So(isOpenCodeBaseURL(""), ShouldBeFalse)
			So(isOpenCodeBaseURL("https://opencode.ai.evil.com/v1"), ShouldBeFalse)
		})

		Convey("nil meta 不 panic", func() {
			So(IsOpenCodeChannel(nil), ShouldBeFalse)
		})
	})
}

func TestSetupOpenCodeHeaders(t *testing.T) {
	Convey("OpenCode 渠道注入会话头", t, func() {
		m := &meta.Meta{
			ChannelId:       23,
			ChannelType:     channeltype.OpenCodeGo,
			ActualModelName: "deepseek-v4.1-flash",
		}

		Convey("注入 x-opencode-session 与专用 UA", func() {
			h := http.Header{}
			SetupOpenCodeHeaders(&h, m)
			So(h.Get(openCodeSessionHeader), ShouldNotBeBlank)
			So(h.Get("User-Agent"), ShouldEqual, openCodeUserAgent)
		})

		Convey("同一渠道+模型复用同一会话 ID（prompt-cache 亲和）", func() {
			h1 := http.Header{}
			SetupOpenCodeHeaders(&h1, m)
			h2 := http.Header{}
			SetupOpenCodeHeaders(&h2, m)
			So(h1.Get(openCodeSessionHeader), ShouldEqual, h2.Get(openCodeSessionHeader))
		})

		Convey("不同渠道各自独立会话 ID", func() {
			other := &meta.Meta{
				ChannelId:       99,
				ChannelType:     channeltype.OpenCodeGo,
				ActualModelName: "deepseek-v4.1-flash",
			}
			h1 := http.Header{}
			SetupOpenCodeHeaders(&h1, m)
			h2 := http.Header{}
			SetupOpenCodeHeaders(&h2, other)
			So(h1.Get(openCodeSessionHeader), ShouldNotEqual, h2.Get(openCodeSessionHeader))
		})

		Convey("调用方已带会话头时尊重原值，不覆盖", func() {
			h := http.Header{}
			h.Set(openCodeSessionHeader, "ses_caller_supplied")
			SetupOpenCodeHeaders(&h, m)
			So(h.Get(openCodeSessionHeader), ShouldEqual, "ses_caller_supplied")
		})

		Convey("非 OpenCode 渠道不注入", func() {
			h := http.Header{}
			SetupOpenCodeHeaders(&h, &meta.Meta{ChannelType: channeltype.OpenAI})
			So(h.Get(openCodeSessionHeader), ShouldBeBlank)
		})

		Convey("nil header 不 panic", func() {
			So(func() { SetupOpenCodeHeaders(nil, m) }, ShouldNotPanic)
		})
	})
}

func TestOpenCodeSessionRotationAndPrune(t *testing.T) {
	Convey("会话桶轮换与清理", t, func() {
		// 模拟过期桶，验证 prune 会回收（防 map 无界增长）
		openCodeSessionMu.Lock()
		save := openCodeSessions
		openCodeSessions = map[string]*sessionBucket{}
		stale := time.Now().Add(-2 * openCodeSessionTTL)
		for i := 0; i < 128; i++ {
			openCodeSessions[strconv.Itoa(i)] = &sessionBucket{id: "x", refreshed: stale}
		}
		pruneOpenCodeSessionsLocked(time.Now())
		left := len(openCodeSessions)
		openCodeSessions = save
		openCodeSessionMu.Unlock()
		So(left, ShouldEqual, 0)
	})
}
