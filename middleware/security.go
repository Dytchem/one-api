package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为所有响应添加安全头，降低 XSS / 点击劫持 / MIME 嗅探风险
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-XSS-Protection", "1; mode=block")
		// 允许 inline style（KaTeX 依赖）、图片等；仅限制脚本来源
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; connect-src 'self' https:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		// HSTS：仅在 HTTPS 下启用（由反向代理 TLS 终止，此处统一设置无害）
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}
