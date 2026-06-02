package middleware

import (
	"os"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	// dyt-27: 移除 AllowAllOrigins + AllowCredentials 同时开启的 CSRF 风险
	// 通过 ALLOWED_ORIGINS 环境变量配置白名单（逗号分隔）
	// 留空：允许任意 origin 跨域（不带 cookie）—— 降级安全策略，保证 API 仍可用
	allowedOriginsStr := os.Getenv("ALLOWED_ORIGINS")
	var allowedOrigins []string
	if allowedOriginsStr != "" {
		for _, o := range strings.Split(allowedOriginsStr, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowedOrigins = append(allowedOrigins, o)
			}
		}
	}
	if len(allowedOrigins) > 0 {
		config.AllowOrigins = allowedOrigins
	} else {
		// 没有白名单：禁止 AllowCredentials，避免 AllowAllOrigins+Credentials 组合的 CSRF 风险
		config.AllowAllOrigins = true
		config.AllowCredentials = false
	}
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	return cors.New(config)
}
