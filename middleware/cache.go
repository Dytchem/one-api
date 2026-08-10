package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func Cache() func(c *gin.Context) {
	return func(c *gin.Context) {
		uri := c.Request.RequestURI
		switch {
		case uri == "/":
			// HTML 入口禁止一切缓存（no-store）：旧 index.html 被浏览器/CDN
			// 缓存会导致加载旧版本前端，出现“版本回退 / 反复提示更新”。
			c.Header("Cache-Control", "no-store")
		case strings.HasPrefix(uri, "/static/"):
			// 带内容哈希的文件名天然防串版，可放心长缓存 + immutable
			c.Header("Cache-Control", "public, max-age=604800, immutable") // one week
		default:
			// API 与其余动态内容：不缓存
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}
