package ctxkey

const (
	Config            = "config"
	Id                = "id"
	Username          = "username"
	Role              = "role"
	Status            = "status"
	Channel           = "channel"
	ChannelId         = "channel_id"
	SpecificChannelId = "specific_channel_id"
	RequestModel      = "request_model"
	ConvertedRequest  = "converted_request"
	OriginalModel     = "original_model"
	ActualModel       = "actual_model" // dyt-22: 映射后的渠道真实模型名
	Group             = "group"
	ModelMapping      = "model_mapping"
	ChannelName       = "channel_name"
	TokenId           = "token_id"
	TokenName         = "token_name"
	BaseURL           = "base_url"
	AvailableModels   = "available_models"
	KeyRequestBody    = "key_request_body"
	SystemPrompt      = "system_prompt"
	// dyt-113: 入口协议标记。客户端用的是哪种原生协议，决定响应以哪种格式回写。
	InboundProtocol = "inbound_protocol"
)

// dyt-113: InboundProtocol 的取值。
const (
	// InboundAnthropic 表示客户端用的是 Anthropic Messages 协议（POST /v1/messages）
	InboundAnthropic = "anthropic"
	// InboundGeminiInteractions 表示客户端用的是 Gemini Interactions 协议
	InboundGeminiInteractions = "gemini_interactions"
)
