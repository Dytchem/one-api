package model

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`

	// dyt-40: 缓存字段（只记录，不定价）—— 上游各厂 cache 字段都汇总到这 4 个
	// OpenAI: prompt_tokens_details.cached_tokens
	// Anthropic: usage.cache_read_input_tokens
	// DeepSeek: usage.prompt_cache_hit_tokens
	// Gemini: usageMetadata.cachedContentTokenCount
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`

	// Anthropic: usage.cache_creation_input_tokens（5min + 1h 合并）
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`

	// Anthropic 5min TTL: usage.cache_creation.ephemeral_5m_input_tokens
	CacheCreation5mTokens int `json:"cache_creation_5m_tokens,omitempty"`

	// Anthropic 1h TTL: usage.cache_creation.ephemeral_1h_input_tokens
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens,omitempty"`

	// dyt-40: DeepSeek 原始字段（post-process 后写入 CacheReadTokens）
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`

	// dyt-40: Gemini 原始字段
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
}

// dyt-40: OpenAI 兼容协议的 prompt 侧详情（DeepSeek 也用这个格式）
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens"`
}

type Error struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Code    any    `json:"code"`
}

type ErrorWithStatusCode struct {
	Error
	StatusCode int `json:"status_code"`
}
