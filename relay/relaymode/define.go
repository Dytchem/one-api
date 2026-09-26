package relaymode

const (
	Unknown = iota
	ChatCompletions
	Completions
	Embeddings
	Moderations
	ImagesGenerations
	Edits
	AudioSpeech
	AudioTranscription
	AudioTranslation
	// Proxy is a special relay mode for proxying requests to custom upstream
	Proxy
	// Responses is the OpenAI Responses API (https://platform.openai.com/docs/api-reference/responses)
	Responses
	// dyt-113: Anthropic Messages API 原生入口（POST /v1/messages），出入参均为 Anthropic 格式
	AnthropicMessages
	// dyt-113: Gemini Interactions API 原生入口（POST /v1beta/interactions），
	// Google 已于 2026-06 GA 并作为 Gemini 主推入口
	GeminiInteractions
)
