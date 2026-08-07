package middleware

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// SameOriginGuard 拦截跨站的带 cookie 状态变更请求（CSRF 防护）。
// dyt-104: email/wechat 绑定等接口是 GET + session cookie 鉴权，
// SameSite=Lax 不阻止顶层 GET 导航携带 cookie，攻击者可诱导受害者访问
// 构造链接完成"绑定攻击者邮箱 → 密码重置 → 接管账号"的利用链。
// 校验规则：Origin 或 Referer 至少存在其一，且其 host 与当前请求 host 一致；
// 两者皆缺（如 rel=noreferrer 链接/隐私扩展剥离）一律拒绝——
// 受保护的接口均为站内按钮触发的 XHR/fetch，浏览器必然携带 Referer。
func SameOriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqHost := c.Request.Host
		origin := c.Request.Header.Get("Origin")
		referer := c.Request.Header.Get("Referer")
		if origin == "" && referer == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "缺少来源信息，请从页面内正常操作（禁止跨站/匿名调用）",
			})
			c.Abort()
			return
		}
		sameOrigin := false
		if origin != "" {
			if u, err := url.Parse(origin); err == nil && strings.EqualFold(u.Host, reqHost) {
				sameOrigin = true
			}
		}
		if !sameOrigin && referer != "" {
			if u, err := url.Parse(referer); err == nil && strings.EqualFold(u.Host, reqHost) {
				sameOrigin = true
			}
		}
		if !sameOrigin {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "拒绝跨站请求，请从本站页面内操作",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

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
		// dyt-101: font-src 显式声明 —— semantic-ui 的 Dropdown 箭头用 data: URI 内嵌字体，
		// 缺 font-src 时回退 default-src 'self' 会拦截 data: 字体，下拉图标显示为方块
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob: https: http:; connect-src 'self' https: http:; frame-src 'self' https:; frame-ancestors 'self'; base-uri 'self'; form-action 'self'; object-src 'none'")
		// HSTS：仅建议在 HTTPS 反代部署时启用（通过环境变量 SESSION_COOKIE_SECURE 一并控制）
		if os.Getenv("SESSION_COOKIE_SECURE") == "true" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		c.Next()
	}
}
