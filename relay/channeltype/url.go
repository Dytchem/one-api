package channeltype

var ChannelBaseURLs = []string{
	"",                              // 0
	"https://api.openai.com",        // 1
	"https://oa.api2d.net",          // 2
	"",                              // 3
	"https://api.closeai-proxy.xyz", // 4
	"https://api.openai-sb.com",     // 5
	"https://api.openaimax.com",     // 6
	"https://api.ohmygpt.com",       // 7
	"",                              // 8
	"https://api.caipacity.com",     // 9
	"https://api.aiproxy.io",        // 10
	"https://generativelanguage.googleapis.com", // 11
	"https://api.api2gpt.com",                   // 12
	"https://api.aigc2d.com",                    // 13
	"https://api.anthropic.com",                 // 14
	"https://aip.baidubce.com",                  // 15 文心 V1（已停服，推荐 V2）
	"https://open.bigmodel.cn",                  // 16
	"https://dashscope.aliyuncs.com",            // 17
	"",                                          // 18
	"https://api.360.cn",                        // 19
	"https://openrouter.ai/api",                 // 20
	"https://api.aiproxy.io",                    // 21
	"https://fastgpt.run/api/openapi",           // 22
	"https://api.hunyuan.cloud.tencent.com/v1",  // 23 腾讯混元 OpenAI 兼容端点（原 TC3 已停售）
	"https://generativelanguage.googleapis.com", // 24
	"https://api.moonshot.cn",                   // 25
	"https://api.baichuan-ai.com",               // 26
	"https://api.minimaxi.com",                  // 27 MiniMax 国内新域名（原 minimax.chat 已停用）
	"https://api.mistral.ai",                    // 28
	"https://api.groq.com/openai",               // 29
	"http://localhost:11434",                    // 30
	"https://api.lingyiwanwu.com",               // 31
	"https://api.stepfun.com",                   // 32
	"",                                          // 33
	"https://api.coze.cn",                       // 34 Coze 国内（国际用 api.coze.com）
	"https://api.cohere.ai",                     // 35
	"https://api.deepseek.com",                  // 36
	"https://api.cloudflare.com",                // 37
	"https://api-free.deepl.com",                // 38
	"https://api.together.xyz",                  // 39
	"https://ark.cn-beijing.volces.com",         // 40
	"https://api.novita.ai/v3/openai",           // 41
	"",                                          // 42
	"",                                          // 43
	"https://api.siliconflow.cn",                // 44
	"https://api.x.ai",                          // 45
	"https://api.replicate.com/v1/models/",      // 46
	"https://qianfan.baidubce.com",              // 47
	"https://spark-api-open.xf-yun.com",         // 48
	"https://dashscope.aliyuncs.com",            // 49
	"",                                          // 50

	"https://generativelanguage.googleapis.com/v1beta/openai/", // 51

	"https://api.perplexity.ai",                 // 52 Perplexity
	"https://api.mok.ai",                        // 53 MokaAI
	"",                                          // 54 Xinference（需自填 base_url）
	"https://api.cerebras.ai/v1",                // 55 Cerebras
	"https://api.hyperbolic.xyz/v1",             // 56 Hyperbolic
	"https://api.fireworks.ai/inference/v1",     // 57 Fireworks AI
	"https://api.lambdalabs.com/v1",             // 58 Lambda
	"https://open.bigmodel.cn/api/paas/v4",      // 59 智谱 GLM（OpenAI 兼容 v4）
	"https://api.jina.ai",                       // 60 Jina
}

func init() {
	if len(ChannelBaseURLs) != Dummy {
		panic("channel base urls length not match")
	}
}
