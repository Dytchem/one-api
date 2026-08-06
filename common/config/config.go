package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common/env"

	"github.com/google/uuid"
)

var SystemName = "One API"
var ServerAddress = "http://localhost:3000"
var Footer = ""
var Logo = ""
var TopUpLink = ""
var ChatLink = ""
var QuotaPerUnit = 500 * 1000.0 // $0.002 / 1K tokens
var DisplayInCurrencyEnabled = true
var DisplayTokenStatEnabled = true

// Any options with "Secret", "Token" in its key won't be return by GetOptions

// SessionSecret 由 common/init.go 处理：
// - 如果设了 SESSION_SECRET 环境变量（≠"random_string"）→ 用它
// - 否则 → uuid.New()（重启后所有 session 失效）
// 推荐主人设一个 ≥32 字符的随机串
var SessionSecret = uuid.New().String()

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 100

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false
var GitHubOAuthEnabled = false
var OidcEnabled = false
var WeChatAuthEnabled = false
var TurnstileCheckEnabled = false
var RegisterEnabled = true

var EmailDomainRestrictionEnabled = false
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}

var DebugEnabled = strings.ToLower(os.Getenv("DEBUG")) == "true"
var DebugSQLEnabled = strings.ToLower(os.Getenv("DEBUG_SQL")) == "true"
var MemoryCacheEnabled = strings.ToLower(os.Getenv("MEMORY_CACHE_ENABLED")) == "true"

var LogConsumeEnabled = true

var SMTPServer = ""
var SMTPPort = 587
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var GitHubClientId = ""
var GitHubClientSecret = ""

var LarkClientId = ""
var LarkClientSecret = ""

var OidcClientId = ""
var OidcClientSecret = ""
var OidcWellKnown = ""
var OidcAuthorizationEndpoint = ""
var OidcTokenEndpoint = ""
var OidcUserinfoEndpoint = ""

var WeChatServerAddress = ""
var WeChatServerToken = ""
var WeChatAccountQRCodeImageURL = ""

var MessagePusherAddress = ""
var MessagePusherToken = ""

var TurnstileSiteKey = ""
var TurnstileSecretKey = ""

var QuotaForNewUser int64 = 0
var QuotaForInviter int64 = 0
var QuotaForInvitee int64 = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false
var QuotaRemindThreshold int64 = 1000
var PreConsumedQuota int64 = 500
var ApproximateTokenEnabled = false
var RetryTimes = 0

var RootUserEmail = ""

var IsMasterNode = os.Getenv("NODE_TYPE") != "slave"

var requestInterval, _ = strconv.Atoi(os.Getenv("POLLING_INTERVAL"))
var RequestInterval = time.Duration(requestInterval) * time.Second

var SyncFrequency = env.Int("SYNC_FREQUENCY", 10*60) // unit is second

var BatchUpdateEnabled = false
var BatchUpdateInterval = env.Int("BATCH_UPDATE_INTERVAL", 5)

// dyt-31: RelayTimeout 默认 300s（5分钟），原默认 0=无超时会被恶意上游永久卡住
// 5 分钟足够任何 LLM 响应（即使最慢的 reasoning 模型也 30s 内）
// 主人如果调过 RELAY_TIMEOUT 环境变量，本默认值会被覆盖
var RelayTimeout = env.Int("RELAY_TIMEOUT", 300) // unit is second

// dyt-33: log_payloads 默认 7 天后自动清理
// 设 LOG_PAYLOAD_TTL_HOURS=0 禁用清理（不推荐，payload 会无限增长）
var LogPayloadTTLHours = env.Int("LOG_PAYLOAD_TTL_HOURS", 7*24) // unit is hour

var GeminiSafetySetting = env.String("GEMINI_SAFETY_SETTING", "BLOCK_NONE")

var Theme = env.String("THEME", "default")
var ValidThemes = map[string]bool{
	"default": true,
	"berry":   true,
	"air":     true,
}

// All duration's unit is seconds
// Shouldn't larger then RateLimitKeyExpirationDuration
var (
	GlobalApiRateLimitNum            = env.Int("GLOBAL_API_RATE_LIMIT", 480)
	GlobalApiRateLimitDuration int64 = 3 * 60

	GlobalWebRateLimitNum            = env.Int("GLOBAL_WEB_RATE_LIMIT", 240)
	GlobalWebRateLimitDuration int64 = 3 * 60

	UploadRateLimitNum            = 10
	UploadRateLimitDuration int64 = 60

	DownloadRateLimitNum            = 10
	DownloadRateLimitDuration int64 = 60

	CriticalRateLimitNum            = 20
	CriticalRateLimitDuration int64 = 20 * 60
)

var RateLimitKeyExpirationDuration = 20 * time.Minute

var EnableMetric = env.Bool("ENABLE_METRIC", false)
var MetricQueueSize = env.Int("METRIC_QUEUE_SIZE", 10)
var MetricSuccessRateThreshold = env.Float64("METRIC_SUCCESS_RATE_THRESHOLD", 0.8)
var MetricSuccessChanSize = env.Int("METRIC_SUCCESS_CHAN_SIZE", 1024)
var MetricFailChanSize = env.Int("METRIC_FAIL_CHAN_SIZE", 128)

// Channel health metrics: sliding window + circuit breaker
var ChannelHealthEnabled = env.Bool("CHANNEL_HEALTH_ENABLED", true)
var ChannelHealthWindowSize = env.Int("CHANNEL_HEALTH_WINDOW_SIZE", 20)      // 滑动窗口大小
var ChannelHealthFailWeight = env.Float64("CHANNEL_HEALTH_FAIL_WEIGHT", 3.0) // 失败权重（失败一次相当于失败权重次成功）
var CircuitBreakerThreshold = env.Int("CIRCUIT_BREAKER_THRESHOLD", 3)        // 连续失败熔断阈值
var CircuitBreakerCooldown = env.Int("CIRCUIT_BREAKER_COOLDOWN", 60)         // 熔断冷却时间（秒）

var InitialRootToken = os.Getenv("INITIAL_ROOT_TOKEN")

var InitialRootAccessToken = os.Getenv("INITIAL_ROOT_ACCESS_TOKEN")

// dyt-64: pi agent 桥接服务地址（http://127.0.0.1:3005）
// AgentBridgeURL 默认容器内自部署的 pi-bridge（同容器/同机部署无需配置）；
// 与 entrypoint.sh 的 BRIDGE_PORT 默认值 3005 保持一致
var AgentBridgeURL = func() string {
	if v := os.Getenv("AGENT_BRIDGE_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:3005"
}()

// dyt-96: bridge 共享密钥。必须与 pi-bridge 的 BRIDGE_SECRET 一致，
// 请求 bridge 时通过 X-Bridge-Token 头携带；bridge 侧缺失该密钥时拒绝一切请求，
// 防止同机其他进程/被 SSRF 的服务伪造 user_id 调用 bridge 工具
var AgentBridgeSecret = env.String("AGENT_BRIDGE_SECRET", "")

var GeminiVersion = env.String("GEMINI_VERSION", "v1")

var OnlyOneLogFile = env.Bool("ONLY_ONE_LOG_FILE", false)

var RelayProxy = env.String("RELAY_PROXY", "")
var UserContentRequestProxy = env.String("USER_CONTENT_REQUEST_PROXY", "")
var UserContentRequestTimeout = env.Int("USER_CONTENT_REQUEST_TIMEOUT", 30)

// dyt-48: SSE probe 首 token 超时（流式探测上游时等待第一个 data: 内容的最大时间）
var ProbeTimeout = env.Int("PROBE_TIMEOUT", 120)

// dyt-52: 流式 SSE 单行缓冲上限（MB），超长行（base64 图片）不截断；0 表示使用默认 64MB
// dyt-96: 默认 8MB（原 64MB 在异常上游单行灌入时易造成内存尖峰）
var StreamScannerMaxBufferMB = env.Int("STREAM_SCANNER_MAX_BUFFER_MB", 8)

// dyt-52: 请求体大小上限（MB），防止超大请求/zip bomb 打爆内存；0 表示不限制
var MaxRequestBodyMB = env.Int("MAX_REQUEST_BODY_MB", 32)

// dyt-52: 流式请求整体超时（秒）。0 表示跟随 HTTPClient.Timeout（默认 300s）
var StreamingTimeout = env.Int("STREAMING_TIMEOUT", 0)

var EnforceIncludeUsage = env.Bool("ENFORCE_INCLUDE_USAGE", false)
var TestPrompt = env.String("TEST_PROMPT", "Output only your specific model name with no additional text.")
