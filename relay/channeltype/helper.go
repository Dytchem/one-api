package channeltype

import "github.com/songquanpeng/one-api/relay/apitype"

func ToAPIType(channelType int) int {
	apiType := apitype.OpenAI
	switch channelType {
	case Anthropic:
		apiType = apitype.Anthropic
	case Baidu:
		apiType = apitype.Baidu
	case PaLM:
		apiType = apitype.PaLM
	case Zhipu:
		apiType = apitype.Zhipu
	case Ali:
		apiType = apitype.Ali
	case Xunfei:
		apiType = apitype.Xunfei
	case AIProxyLibrary:
		apiType = apitype.AIProxyLibrary
	case Tencent:
		// dyt-53: 腾讯混元已停售 TC3 签名协议，官方仅保留 OpenAI 兼容端点
		apiType = apitype.OpenAI
	case Gemini:
		apiType = apitype.Gemini
	case Ollama:
		apiType = apitype.Ollama
	case AwsClaude:
		apiType = apitype.AwsClaude
	case Coze:
		apiType = apitype.Coze
	case Cohere:
		apiType = apitype.Cohere
	case Cloudflare:
		apiType = apitype.Cloudflare
	case DeepL:
		apiType = apitype.DeepL
	case VertextAI:
		apiType = apitype.VertexAI
	case Replicate:
		apiType = apitype.Replicate
	case Proxy:
		apiType = apitype.Proxy
	}

	return apiType
}
