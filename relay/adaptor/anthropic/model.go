package anthropic

// https://docs.anthropic.com/claude/reference/messages_post

type Metadata struct {
	UserId string `json:"user_id"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type Content struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
	// tool_calls
	Id        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	Content   string `json:"content,omitempty"`
	ToolUseId string `json:"tool_use_id,omitempty"`
}

type Message struct {
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema InputSchema `json:"input_schema"`
}

type InputSchema struct {
	Type       string `json:"type"`
	Properties any    `json:"properties,omitempty"`
	Required   any    `json:"required,omitempty"`
}

type Request struct {
	Model         string    `json:"model"`
	Messages      []Message `json:"messages"`
	System        string    `json:"system,omitempty"`
	MaxTokens     int       `json:"max_tokens,omitempty"`
	StopSequences []string  `json:"stop_sequences,omitempty"`
	Stream        bool      `json:"stream,omitempty"`
	Temperature   *float64  `json:"temperature,omitempty"`
	TopP          *float64  `json:"top_p,omitempty"`
	TopK          int       `json:"top_k,omitempty"`
	Tools         []Tool    `json:"tools,omitempty"`
	ToolChoice    any       `json:"tool_choice,omitempty"`
	//Metadata    `json:"metadata,omitempty"`

	// dyt-114: 扩展思考（extended thinking / adaptive thinking）。
	// 原实现完全丢弃该字段，导致客户端开了思考而网关发出去的请求里没有，
	// 上游按"未开启思考"处理——用户以为在用 thinking，实际没生效。
	// 形如 {"type":"enabled","budget_tokens":10000} 或 {"type":"adaptive"}。
	Thinking any `json:"thinking,omitempty"`
	// dyt-114: 上下文管理（服务端压缩 compaction / context editing）。
	// 形如 {"edits":[{"type":"compact_20260112"}]}，需配合 anthropic-beta 头。
	ContextManagement any `json:"context_management,omitempty"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`     // dyt-40
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"` // dyt-40
	// dyt-40: 新版 Anthropic 区分 5min / 1h TTL
	CacheCreation *CacheCreation `json:"cache_creation,omitempty"`
}

// dyt-40: Anthropic 5min / 1h 区分
type CacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type Response struct {
	Id           string    `json:"id"`
	Type         string    `json:"type"`
	Role         string    `json:"role"`
	Content      []Content `json:"content"`
	Model        string    `json:"model"`
	StopReason   *string   `json:"stop_reason"`
	StopSequence *string   `json:"stop_sequence"`
	Usage        Usage     `json:"usage"`
	Error        Error     `json:"error"`
}

type Delta struct {
	Type         string  `json:"type"`
	Text         string  `json:"text"`
	PartialJson  string  `json:"partial_json,omitempty"`
	StopReason   *string `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type StreamResponse struct {
	Type         string    `json:"type"`
	Message      *Response `json:"message"`
	Index        int       `json:"index"`
	ContentBlock *Content  `json:"content_block"`
	Delta        *Delta    `json:"delta"`
	Usage        *Usage    `json:"usage"`
}
