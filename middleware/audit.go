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

var sensitiveFieldRegex = regexp.MustCompile(`(?i)("(?:key|password|secret|access_token|authorization|token)"\s*:\s*")[^"]*(")`)

// option 配置项中需要打码 value 的敏感 key（按后缀匹配）
var sensitiveOptionKeyRegex = regexp.MustCompile(`(?i)(secret|token|key|password|dsn|client_id)`)

// redactBody 对审计日志中的请求体脱敏：
// 1. JSON 字段 key/password/secret/token 等 → 值打码
// 2. option 更新请求 {"key":"SMTPToken","value":"xxx"} → key 命中敏感名单时 value 打码
// 3. 表单编码 api_key=xxx 形式 → 打码
func redactBody(raw []byte) string {
	s := string(raw)
	s = sensitiveFieldRegex.ReplaceAllString(s, `${1}***${3}`)
	// {"key":"...","value":"..."} 结构
	s = regexp.MustCompile(`(?i)("key"\s*:\s*")([^"]*)(",\s*"value"\s*:\s*")([^"]*)(")`).ReplaceAllStringFunc(s, func(m string) string {
		parts := regexp.MustCompile(`(?i)("key"\s*:\s*")([^"]*)(",\s*"value"\s*:\s*")([^"]*)(")`).FindStringSubmatch(m)
		if len(parts) == 6 && sensitiveOptionKeyRegex.MatchString(parts[2]) {
			return parts[1] + parts[2] + parts[3] + "***" + parts[5]
		}
		return m
	})
	// 表单编码
	s = regexp.MustCompile(`(?i)(api_key|password|secret|token|key)=[^&\s]+`).ReplaceAllString(s, `${1}=***`)
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
