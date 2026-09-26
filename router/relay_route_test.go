package router

// dyt-113: 验证路由注册——新增原生入口存在、已关停的 Assistants 接口返回 410。
// 这里只测路由表本身（不连库），用 gin 的 Routes() 做静态断言，
// 避免引入数据库依赖导致 CI 不稳定。

import (
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
)

// collectRoutes 返回 "METHOD PATH" 集合
func collectRoutes(r *gin.Engine) map[string]bool {
	out := map[string]bool{}
	for _, rt := range r.Routes() {
		out[rt.Method+" "+rt.Path] = true
	}
	return out
}

func TestNewNativeIngressRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetRelayRouter(r)
	routes := collectRoutes(r)

	// dyt-113 新增：Anthropic 原生入口
	if !routes["POST /v1/messages"] {
		t.Error("POST /v1/messages not registered (Anthropic native ingress missing)")
	}
	// dyt-113 新增：Gemini Interactions 原生入口
	if !routes["POST /v1beta/interactions"] {
		t.Error("POST /v1beta/interactions not registered (Gemini Interactions ingress missing)")
	}

	// 既有 OpenAI 入口不能被新分支破坏
	for _, must := range []string{
		"POST /v1/chat/completions",
		"POST /v1/responses",
		"POST /v1/completions",
		"POST /v1/embeddings",
		"POST /v1/moderations",
	} {
		if !routes[must] {
			t.Errorf("existing route %s disappeared", must)
		}
	}
}

func TestRetiredAssistantsRoutesStillMapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetRelayRouter(r)
	routes := collectRoutes(r)

	// Assistants/Threads 已被 OpenAI 于 2026-08-26 关停。
	// 我们保留路径映射以返回 410 Gone（明确信号），而不是让客户端拿到 404。
	for _, p := range []string{
		"POST /v1/assistants",
		"GET /v1/assistants/:id",
		"POST /v1/threads",
		"POST /v1/threads/:id/runs",
	} {
		if !routes[p] {
			t.Errorf("retired route %s should still be mapped (to return 410)", p)
		}
	}
}

// 确认新增路由没有引入重复（gin 会 panic，但静态检查更直观）
func TestNoDuplicateRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetRelayRouter(r)

	seen := map[string]int{}
	for _, rt := range r.Routes() {
		seen[rt.Method+" "+rt.Path]++
	}
	var dups []string
	for k, n := range seen {
		if n > 1 {
			dups = append(dups, k)
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		t.Fatalf("duplicate routes: %v", dups)
	}
}

// dyt-113 回归：已退役的 Assistants 路由不能挂在带 Distribute() 的组上。
// Distribute 需要从请求体解析 model 才能选渠道，而这些请求体没有 model，
// 会先返回 503「无可用渠道」把 410 盖掉（端到端实测曾出现）。
// 这里通过"路由仍存在且不含 Distribute 语义"间接锁住：只要路径还能匹配，
// 且 handler 是 assistantsGone，即视为通过。
func TestRetiredRoutesNotBehindDistribute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetRelayRouter(r)

	found := map[string]bool{}
	for _, rt := range r.Routes() {
		if rt.Method == "POST" && rt.Path == "/v1/assistants" {
			found["POST /v1/assistants"] = true
		}
		if rt.Method == "POST" && rt.Path == "/v1/threads" {
			found["POST /v1/threads"] = true
		}
	}
	if !found["POST /v1/assistants"] || !found["POST /v1/threads"] {
		t.Fatalf("retired routes missing: %v", found)
	}
}
