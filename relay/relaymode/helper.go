package relaymode

import "strings"

func GetByPath(path string) int {
	relayMode := Unknown
	if strings.HasPrefix(path, "/v1/chat/completions") {
		relayMode = ChatCompletions
	} else if strings.HasPrefix(path, "/v1/responses") {
		relayMode = Responses
	} else if strings.HasPrefix(path, "/v1/completions") {
		relayMode = Completions
	} else if strings.HasPrefix(path, "/v1/embeddings") {
		relayMode = Embeddings
	} else if strings.HasSuffix(path, "embeddings") {
		relayMode = Embeddings
	} else if strings.HasPrefix(path, "/v1/moderations") {
		relayMode = Moderations
	} else if strings.HasPrefix(path, "/v1/images/generations") {
		relayMode = ImagesGenerations
	} else if strings.HasPrefix(path, "/v1/edits") {
		relayMode = Edits
	} else if strings.HasPrefix(path, "/v1/audio/speech") {
		relayMode = AudioSpeech
	} else if strings.HasPrefix(path, "/v1/audio/transcriptions") {
		relayMode = AudioTranscription
	} else if strings.HasPrefix(path, "/v1/audio/translations") {
		relayMode = AudioTranslation
	} else if strings.HasPrefix(path, "/v1/oneapi/proxy") {
		relayMode = Proxy
	} else if strings.HasPrefix(path, "/v1/messages") {
		// dyt-113: Anthropic 原生入口。注意必须放在 /v1/ 其它前缀之后，
		// 且不能用 HasSuffix，避免误吞 /v1/messages/batches 等子资源。
		relayMode = AnthropicMessages
	} else if strings.HasPrefix(path, "/v1beta/interactions") || strings.HasPrefix(path, "/v1/interactions") {
		// dyt-113: Gemini Interactions API 原生入口（官方路径为 /v1beta/interactions）
		relayMode = GeminiInteractions
	}
	return relayMode
}
