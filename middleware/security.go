package middleware

import (
	"os"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为所有响应添加安全头，降低 XSS / 点击劫持 / MIME 嗅探风险
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// 允许 inline style（KaTeX 依赖）、http/https 图片与 API（支持 LAN 纯 HTTP 部署）；
		// unsafe-eval：CRA/webpack 运行时及部分库（moment/date-fns 等）依赖 new Function
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https: http:; connect-src 'self' https: http:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
		// HSTS：仅建议在 HTTPS 反代部署时启用（通过环境变量 SESSION_COOKIE_SECURE 一并控制）
		if os.Getenv("SESSION_COOKIE_SECURE") == "true" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		c.Next()
	}
}
