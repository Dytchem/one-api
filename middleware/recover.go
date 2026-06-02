package middleware

import (
	"fmt"
	"net/http"
	"os"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

func RelayPanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				ctx := c.Request.Context()
				logger.Errorf(ctx, fmt.Sprintf("panic detected: %v", err))
				logger.Errorf(ctx, fmt.Sprintf("stacktrace from panic: %s", string(debug.Stack())))
				logger.Errorf(ctx, fmt.Sprintf("request: %s %s", c.Request.Method, c.Request.URL.Path))
				// dyt-32: 默认不打印 request body（可能含 API key / 密码 / 用户 prompt 等敏感信息）
				// 主人 debug 时设 PANIC_LOG_BODY=true 才打印
				if os.Getenv("PANIC_LOG_BODY") == "true" {
					body, _ := common.GetRequestBody(c)
					logger.Errorf(ctx, fmt.Sprintf("request body: %s", string(body)))
				} else {
					logger.Errorf(ctx, "request body suppressed (set PANIC_LOG_BODY=true to enable)")
				}
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Panic detected, error: %v. Please submit an issue with the related log here: https://github.com/songquanpeng/one-api", err),
						"type":    "one_api_panic",
					},
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
