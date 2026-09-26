package router

import (
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/controller"
	"github.com/songquanpeng/one-api/middleware"

	"net/http"
)

// bodySizeLimit 限制请求体大小，防止超大请求/zip bomb 打爆内存（0 表示不限制）
func bodySizeLimit() gin.HandlerFunc {
	maxBytes := int64(config.MaxRequestBodyMB) * 1024 * 1024
	if maxBytes <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.GzipDecodeMiddleware())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	// dyt-106: 这组路由挂在 relayV1Router 之外，若不单独挂限流则 /v1/models
	// 成为"随机 sk- 洪峰"仍可无上限打库的缺口（TokenAuth 未命中缓存时每请求查一次库）。
	modelsRouter.Use(middleware.RelayRateLimit(), middleware.TokenAuth())
	{
		modelsRouter.GET("", controller.ListModels)
		modelsRouter.GET("/:model", controller.RetrieveModel)
	}
	relayV1Router := router.Group("/v1")
	// dyt-93: bodySizeLimit 必须在 TokenAuth 之前（extractChannelId 会先读 body）
	// dyt-106: RelayRateLimit 放在 TokenAuth 之前——它要挡的正是"未命中 token 缓存
	// 的随机 sk- 洪峰"，必须在查库之前生效才有意义。
	relayV1Router.Use(middleware.RelayPanicRecover(), middleware.RelayRateLimit(), bodySizeLimit(), middleware.TokenAuth(), middleware.Distribute())
	{
		// dyt-113: Anthropic Messages API 原生入口。
		// Claude Code / Anthropic SDK 等只能讲 Anthropic 协议的客户端可直接打这里。
		relayV1Router.POST("/messages", controller.Relay)
		// /v1/messages/count_tokens 是 Anthropic 的 token 预估接口，
		// 网关侧不做精确分词，直接拒绝（而不是 404），让客户端能明确降级。
		relayV1Router.POST("/messages/count_tokens", controller.RelayNotImplemented)
		relayV1Router.Any("/oneapi/proxy/:channelid/*target", controller.Relay)
		relayV1Router.POST("/completions", controller.Relay)
		relayV1Router.POST("/chat/completions", controller.Relay)
		relayV1Router.POST("/responses", controller.Relay)
		relayV1Router.POST("/edits", controller.Relay)
		relayV1Router.POST("/images/generations", controller.Relay)
		relayV1Router.POST("/images/edits", controller.RelayNotImplemented)
		relayV1Router.POST("/images/variations", controller.RelayNotImplemented)
		relayV1Router.POST("/embeddings", controller.Relay)
		relayV1Router.POST("/engines/:model/embeddings", controller.Relay)
		relayV1Router.POST("/audio/transcriptions", controller.Relay)
		relayV1Router.POST("/audio/translations", controller.Relay)
		relayV1Router.POST("/audio/speech", controller.Relay)
		relayV1Router.GET("/files", controller.RelayNotImplemented)
		relayV1Router.POST("/files", controller.RelayNotImplemented)
		relayV1Router.DELETE("/files/:id", controller.RelayNotImplemented)
		relayV1Router.GET("/files/:id", controller.RelayNotImplemented)
		relayV1Router.GET("/files/:id/content", controller.RelayNotImplemented)
		relayV1Router.POST("/fine_tuning/jobs", controller.RelayNotImplemented)
		relayV1Router.GET("/fine_tuning/jobs", controller.RelayNotImplemented)
		relayV1Router.GET("/fine_tuning/jobs/:id", controller.RelayNotImplemented)
		relayV1Router.POST("/fine_tuning/jobs/:id/cancel", controller.RelayNotImplemented)
		relayV1Router.GET("/fine_tuning/jobs/:id/events", controller.RelayNotImplemented)
		relayV1Router.DELETE("/models/:model", controller.RelayNotImplemented)
		relayV1Router.POST("/moderations", controller.Relay)
	}

	// dyt-113: Gemini Interactions API 原生入口。
	// Google 已于 2026-06 将该 API GA 并作为 Gemini 主推入口（路径 /v1beta/interactions）。
	// 单独开 /v1beta 组，中间件与 relayV1Router 保持一致（限流->body 限制->鉴权->分发）。
	relayV1BetaRouter := router.Group("/v1beta")
	relayV1BetaRouter.Use(middleware.RelayPanicRecover(), middleware.RelayRateLimit(), bodySizeLimit(), middleware.TokenAuth(), middleware.Distribute())
	{
		relayV1BetaRouter.POST("/interactions", controller.Relay)
		// 取回带服务端状态的 interaction；网关不代持状态，明确拒绝而不是 404
		relayV1BetaRouter.GET("/interactions/:id", controller.RelayNotImplemented)
		relayV1BetaRouter.DELETE("/interactions/:id", controller.RelayNotImplemented)
		relayV1BetaRouter.POST("/interactions/:id/cancel", controller.RelayNotImplemented)
	}

	// dyt-113: OpenAI Assistants / Threads 已于 2026-08-26 全面关停，相关路由整体移除。
	// 保留 410 Gone 以便老客户端拿到明确信号（而不是 404 导致误判为路径写错）。
	assistantsGone := func(c *gin.Context) {
		c.JSON(http.StatusGone, gin.H{
			"error": gin.H{
				"message": "The Assistants API was shut down by OpenAI on 2026-08-26. Migrate to the Responses API (/v1/responses).",
				"type":    "assistants_api_retired",
				"code":    "assistants_api_retired",
			},
		})
	}
	for _, p := range []string{"/assistants", "/assistants/:id", "/assistants/:id/files",
		"/assistants/:id/files/:fileId", "/threads", "/threads/:id", "/threads/:id/messages",
		"/threads/:id/messages/:messageId", "/threads/:id/messages/:messageId/files",
		"/threads/:id/messages/:messageId/files/:filesId", "/threads/:id/runs",
		"/threads/:id/runs/:runsId", "/threads/:id/runs/:runsId/submit_tool_outputs",
		"/threads/:id/runs/:runsId/cancel", "/threads/:id/runs/:runsId/steps",
		"/threads/:id/runs/:runsId/steps/:stepId"} {
		relayV1Router.Any(p, assistantsGone)
	}
}
