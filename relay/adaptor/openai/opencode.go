package openai

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
)

// dyt-104: OpenCode Zen / Go 请求头自动补充
//
// 背景：https://opencode.ai/zen/ 与 https://opencode.ai/zen/go/v1 要求每个请求携带
// `x-opencode-session`，用于上游路由与 prompt-cache 亲和。opencode 原生客户端会自动带上，
// 但任何第三方客户端（本 One API 转发的 OpenAI SDK / curl / Pi agent 等）都不会带，
// 上游直接返回 400：
//
//	{"type":"error","error":{"type":"MissingSessionID",
//	 "message":"Request is missing x-opencode-session and cannot be routed efficiently."}}
//
// 本文件在转发时自动注入该头，使 One API 作为上游聚合网关时无需调用方改造。

const (
	// openCodeSessionHeader 是上游要求的会话标识头
	openCodeSessionHeader = "x-opencode-session"
	// openCodeHostSuffix 用于识别 OpenCode Zen/Go 上游主机（含自定义 base_url 场景）
	openCodeHostSuffix = "opencode.ai"
	// openCodeSessionTTL 会话 ID 的复用窗口：同一窗口内复用同一 ID 以获得 prompt-cache 亲和，
	// 窗口结束后轮换，避免单个 ID 被上游无限累积上下文画像。
	openCodeSessionTTL = 30 * time.Minute
	// openCodeUserAgent 使用专用 UA（不用 SDK 默认值），便于上游侧识别流量来源与排障
	openCodeUserAgent = "one-api/1.0"
)

// sessionBucket 是 (渠道, 模型) 维度上的会话 ID 桶
type sessionBucket struct {
	id        string
	refreshed time.Time
}

var (
	openCodeSessionMu sync.Mutex
	openCodeSessions  = map[string]*sessionBucket{}
)

// IsOpenCodeChannel 判断渠道是否为 OpenCode Zen / Go。
// 同时兼容渠道类型判定与 base_url 判定：用户可能用 OpenAICompatible 类型直填 opencode.ai 域名。
func IsOpenCodeChannel(m *meta.Meta) bool {
	if m == nil {
		return false
	}
	switch m.ChannelType {
	case channeltype.OpenCodeZen, channeltype.OpenCodeGo:
		return true
	}
	return isOpenCodeBaseURL(m.BaseURL)
}

// isOpenCodeBaseURL 解析 base_url 主机名，判断是否指向 opencode.ai（含子域）。
func isOpenCodeBaseURL(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		// base_url 可能未带 scheme，补一个再解析
		u, err = url.Parse("https://" + baseURL)
		if err != nil || u.Host == "" {
			return false
		}
	}
	host := strings.ToLower(u.Hostname())
	return host == openCodeHostSuffix || strings.HasSuffix(host, "."+openCodeHostSuffix)
}

// getOpenCodeSession 返回该 (渠道, 模型) 维度的稳定会话 ID。
// 复用窗口内保持同一 ID（路由 + prompt-cache 亲和），过期后轮换。
func getOpenCodeSession(m *meta.Meta) string {
	key := sessionKey(m)
	now := time.Now()

	openCodeSessionMu.Lock()
	defer openCodeSessionMu.Unlock()

	if b, ok := openCodeSessions[key]; ok && now.Sub(b.refreshed) < openCodeSessionTTL {
		b.refreshed = now
		return b.id
	}
	b := &sessionBucket{id: "oneapi-" + uuid.New().String(), refreshed: now}
	openCodeSessions[key] = b
	pruneOpenCodeSessionsLocked(now)
	return b.id
}

func sessionKey(m *meta.Meta) string {
	if m == nil {
		return "unknown"
	}
	model := m.ActualModelName
	if model == "" {
		model = m.OriginModelName
	}
	return strings.Join([]string{
		"ch", itoa(m.ChannelId),
		"t", itoa(m.ChannelType),
		"m", model,
	}, "|")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// pruneOpenCodeSessionsLocked 清理过期桶，避免渠道/模型组合长期累积导致 map 无界增长。
// 调用方必须已持有 openCodeSessionMu。
func pruneOpenCodeSessionsLocked(now time.Time) {
	if len(openCodeSessions) < 64 {
		return
	}
	for k, b := range openCodeSessions {
		if now.Sub(b.refreshed) >= openCodeSessionTTL {
			delete(openCodeSessions, k)
		}
	}
}

// SetupOpenCodeHeaders 为 OpenCode 渠道补上必需的会话头与专用 UA。
// 调用方若已显式设置 x-opencode-session（例如上游调用方自己带了稳定会话），则尊重其值不覆盖。
func SetupOpenCodeHeaders(req *http.Header, m *meta.Meta) {
	if req == nil || !IsOpenCodeChannel(m) {
		return
	}
	if req.Get(openCodeSessionHeader) == "" {
		req.Set(openCodeSessionHeader, getOpenCodeSession(m))
	}
	// 专用 UA：不要用 SDK 默认 UA，便于上游识别与排障
	if req.Get("User-Agent") == "" {
		req.Set("User-Agent", openCodeUserAgent)
	}
}
