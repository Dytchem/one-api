package middleware

import (
	"bytes"
	"io"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
)

var sensitiveFieldRegex = regexp.MustCompile(`(?i)("(?:key|password|secret|access_token|authorization)"\s*:\s*")[^"]*(")`)

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
			if err == nil && len(raw) > 0 {
				body = sensitiveFieldRegex.ReplaceAllString(string(raw), `${1}***${3}`)
				if len(body) > 1024 {
					body = body[:1024] + "...(truncated)"
				}
				c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))
			}
		}
		if body != "" {
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
