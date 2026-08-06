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
		// 前端 AppViewport 用同源 iframe 承载 1440 视口（canvas_inner），
		// 必须允许同源嵌入：SAMEORIGIN（跨域仍禁止，配合 CSP frame-ancestors 'self'）
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// 允许 inline style（KaTeX 依赖）、http/https 图片与 API（支持 LAN 纯 HTTP 部署）；
		// unsafe-inline：react 事件属性依赖；dyt-96 移除 unsafe-eval（react-scripts 5 生产构建无需 eval）
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https: http:; connect-src 'self' https: http:; frame-src 'self' https:; frame-ancestors 'self'; base-uri 'self'; form-action 'self'; object-src 'none'")
		// HSTS：仅建议在 HTTPS 反代部署时启用（通过环境变量 SESSION_COOKIE_SECURE 一并控制）
		if os.Getenv("SESSION_COOKIE_SECURE") == "true" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		c.Next()
	}
}
