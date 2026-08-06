package middleware

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
)

var sensitiveFieldRegex = regexp.MustCompile(`(?i)("(?:key|password|secret|access_token|authorization|api_key|token)"\s*:\s*")([^"]*)(")`)

// option 配置项中需要打码 value 的敏感 key（按后缀匹配）
var sensitiveOptionKeyRegex = regexp.MustCompile(`(?i)(secret|token|password|dsn)`)

// option 更新请求结构：{"key":"...","value":"..."}（容忍字段顺序任意、字段间插）
var optionBodyRegex = regexp.MustCompile(`(?i)"key"\s*:\s*"([^"]*)"`)
var optionValueRegex = regexp.MustCompile(`(?i)"value"\s*:\s*"[^"]*"`)
var optionValuePresenceRegex = regexp.MustCompile(`(?i)"value"\s*:`)

var auditFormFieldRegex = regexp.MustCompile(`(?i)(api_key|password|secret|token|authorization)=[^&\s]+`)

// redactBody 对审计日志中的请求体脱敏：
//  1. option 更新请求 {"key":"SMTPToken","value":"xxx"} → key 命中敏感名单时 value 打码
//     （用打码前的 key 快照判断；随后通用 key 正则会把 option 的 key 也打码，可接受）
//  2. 通用 JSON 字段 key/password/secret/token 等 → 值打码
//  3. 表单编码 api_key=xxx 形式 → 打码
func redactBody(raw []byte) string {
	s := string(raw)

	// 先识别 option 结构：提取 key 判断是否敏感，敏感则把整个 value 字段打码
	keyMatch := optionBodyRegex.FindStringSubmatch(s)
	isOptionStruct := len(keyMatch) == 2 && optionValuePresenceRegex.MatchString(s)
	if isOptionStruct && sensitiveOptionKeyRegex.MatchString(keyMatch[1]) {
		s = optionValueRegex.ReplaceAllString(s, `"value":"***"`)
	}

	// key 字段（渠道 sk- 密钥 / 令牌名 / option key 名）由 sensitiveFieldRegex 统一打码
	// 再通用脱敏
	s = sensitiveFieldRegex.ReplaceAllString(s, `${1}***${3}`)
	s = auditFormFieldRegex.ReplaceAllString(s, `${1}=***`)

	// 按 rune 截断，避免切坏 UTF-8
	runes := []rune(s)
	if len(runes) > 1024 {
		s = string(runes[:1024]) + "...(truncated)"
	}
	return s
}

// AuditLog 记录管理端写操作审计日志（用户、渠道、令牌、选项等变更），敏感字段脱敏
func AuditLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != "POST" && method != "PUT" && method != "DELETE" && method != "PATCH" {
			c.Next()
			return
		}
		username, _ := c.Get("username")
		userID, _ := c.Get("id")
		role, _ := c.Get("role")

		var body string
		if c.Request.Body != nil {
			raw, err := io.ReadAll(c.Request.Body)
			if len(raw) > 0 {
				body = redactBody(raw)
			}
			// 无论读取成败都恢复原 body，避免下游拿到空请求体
			c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))
			if err != nil {
				body += "(body read error: " + err.Error() + ")"
			}
		}
		if body != "" && !strings.HasPrefix(body, "(body read error") {
			logger.SysLogf("[AUDIT] %s %s | user=%v(id=%v role=%v) ip=%s body=%s",
				method, c.Request.URL.Path, username, userID, role, c.ClientIP(), body)
		} else {
			logger.SysLogf("[AUDIT] %s %s | user=%v(id=%v role=%v) ip=%s",
				method, c.Request.URL.Path, username, userID, role, c.ClientIP())
		}
		start := time.Now()
		c.Next()
		logger.SysLogf("[AUDIT] %s %s | status=%d duration=%s",
			method, c.Request.URL.Path, c.Writer.Status(), time.Since(start).Round(time.Millisecond))
	}
}
