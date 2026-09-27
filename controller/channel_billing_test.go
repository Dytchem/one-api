package controller

// dyt-120: 渠道余额查询的两个回归测试。
//
// 背景（真实故障）：Web 控制台的"更新全部渠道余额"按钮对 DeepSeek / OpenRouter
// 这类已实现的渠道毫无作用，因为 updateAllChannelsBalance 用一个与
// updateChannelBalance 的 switch 完全对不上的白名单过滤掉了它们：
//
//	if channel.Type != channeltype.OpenAI && channel.Type != channeltype.Custom {
//	    continue
//	}
//
// 结果是：唯一能查余额的两个渠道恰恰被跳过，而 OpenAI/Custom 走的是
// /dashboard/billing/* 那套早已停用的旧接口。这里锁住"白名单 = switch 分支"
// 这个不变量，并确保每个声称支持的渠道都真的有实现分支。

import (
	"testing"

	"github.com/songquanpeng/one-api/relay/channeltype"
)

func TestSupportsBalanceQueryCoversImplementedChannels(t *testing.T) {
	// 这些渠道在 updateChannelBalance 的 switch 中各有真实实现，批量更新必须包含它们。
	implemented := []int{
		channeltype.OpenAI,
		channeltype.Custom,
		channeltype.CloseAI,
		channeltype.OpenAISB,
		channeltype.AIProxy,
		channeltype.API2GPT,
		channeltype.AIGC2D,
		channeltype.SiliconFlow,
		channeltype.DeepSeek,
		channeltype.OpenRouter,
	}
	for _, ct := range implemented {
		if !supportsBalanceQuery(ct) {
			t.Errorf("channel type %d has an updateChannelBalance branch but is excluded from the batch update", ct)
		}
	}
}

func TestSupportsBalanceQueryExcludesUnimplementedChannels(t *testing.T) {
	// 用户生产环境实际存在、但上游未开放余额 API 的渠道：必须返回 false，
	// 否则批量更新会对每个渠道发一次注定 404 的请求，仅白白拖慢并污染日志。
	unimplemented := []int{
		channeltype.Zhipu,
		channeltype.Moonshot,
		channeltype.Minimax,
		channeltype.Gemini,
		channeltype.GeminiOpenAICompatible,
		channeltype.AliBailian,
		channeltype.Xiaomi,      // MiMo
		channeltype.Agnes,       // 72
		channeltype.OllamaCloud, // 64
		channeltype.OpenCodeGo,  // 63
		channeltype.OpenCodeZen, // 62
		channeltype.Anthropic,
		channeltype.Azure,
	}
	for _, ct := range unimplemented {
		if supportsBalanceQuery(ct) {
			t.Errorf("channel type %d is claimed to support balance query but has no updateChannelBalance branch", ct)
		}
	}
}

// TestSupportsBalanceQueryMatchesSwitch 是所有分支都必须显式出现在
// supportsBalanceQuery 里（而不是靠 default 兜底）的护栏。新增渠道实现时
// 若忘记同步这里，本测试不会失败但上面两个用例的语义会退化——因此这里
// 只断言最关键的一条：Azure 明确不支持（switch 中直接 return error）。
func TestAzureExplicitlyUnsupported(t *testing.T) {
	if supportsBalanceQuery(channeltype.Azure) {
		t.Fatal("Azure has no balance implementation (switch returns an error); it must not be in the batch")
	}
}
