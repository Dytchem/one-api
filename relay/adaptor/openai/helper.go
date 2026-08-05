package openai

import (
	"fmt"
	"strings"

	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/model"
)

func ResponseText2Usage(responseText string, modelName string, promptTokens int) *model.Usage {
	usage := &model.Usage{}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = CountTokenText(responseText, modelName)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func GetFullRequestURL(baseURL string, requestURL string, channelType int) string {
	// dyt-53: base URL 以版本段结尾的渠道（如 api.cerebras.ai/v1、api.hunyuan.cloud.tencent.com/v1、
	// open.bigmodel.cn/api/paas/v4），拼接时去掉请求路径里的 /v1，避免 /v1/v1/... 双重版本
	switch channelType {
	case channeltype.OpenAICompatible,
		channeltype.Cerebras,
		channeltype.Hyperbolic,
		channeltype.Fireworks,
		channeltype.Lambda,
		channeltype.ZhipuV4,
		channeltype.Tencent,
		channeltype.GeminiOpenAICompatible,
		// dyt-54: base URL 以 /v1 结尾的常用提供商
		channeltype.Xiaomi,
		channeltype.OpenCodeZen,
		channeltype.OpenCodeGo,
		channeltype.OllamaCloud,
		channeltype.NvidiaNIM,
		channeltype.HuggingFace,
		channeltype.ModelScope,
		channeltype.DeepInfra,
		channeltype.ZAI,
		channeltype.MoonshotGlobal,
		channeltype.Vultr,
		channeltype.Agnes:
		return fmt.Sprintf("%s%s", strings.TrimSuffix(baseURL, "/"), strings.TrimPrefix(requestURL, "/v1"))
	}
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	if strings.HasPrefix(baseURL, "https://gateway.ai.cloudflare.com") {
		switch channelType {
		case channeltype.OpenAI:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/v1"))
		case channeltype.Azure:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/openai/deployments"))
		}
	}
	return fullRequestURL
}
