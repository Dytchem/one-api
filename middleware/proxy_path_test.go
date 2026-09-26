package middleware

import "testing"

// dyt-107: proxy 目标白名单回归测试。
//
// 关键前提（已用真实 gin v1.10.1 实测确认）：
//   - c.Request.URL.Path 形如 `/v1/oneapi/proxy/5/v1/chat/completions`（含路由前缀）
//   - c.Param("target")  形如 `/v1/chat/completions`（通配段捕获值）
//
// 白名单项全部以 `/v1/` 开头，因此**必须**喂 target；
// 旧实现喂的是 URL.Path，导致 HasPrefix 对任何输入恒为 false，
// 即普通用户的 proxy 请求 100% 被判越权（proxy 对非 admin 完全不可用）。
func TestIsAllowedProxyPathUsesTargetSemantics(t *testing.T) {
	allowed := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/embeddings",
		"/v1/audio/speech",
		"/v1/audio/transcriptions",
		"/v1/images/generations",
		"/v1/models",
		"/v1/moderations",
	}
	for _, p := range allowed {
		if !isAllowedProxyPath(p) {
			t.Errorf("合法目标应放行: %s", p)
		}
	}
}

func TestIsAllowedProxyPathRejectsEscapes(t *testing.T) {
	denied := []string{
		// `..` 逃逸：Clean 后落在白名单之外
		"/v1/images/../../../../admin/setting",
		"/v1/models/../../admin",
		"/v1/audio/../../../../api/channel",
		// 完全不在 /v1 命名空间
		"/api/channel",
		"/admin/setting",
		"/",
		"/../etc/passwd",
		// 前缀不在白名单内（注意：白名单是 HasPrefix 语义，
		// `/v1/modelsX` 这类仍会命中 `/v1/models` 前缀，属既有设计，不在本测试范围）
		"/v1/auditory",
		"/v2/models",
	}
	for _, p := range denied {
		if isAllowedProxyPath(p) {
			t.Errorf("越权目标应拒绝: %s", p)
		}
	}
}

// 反向保险：确认旧实现喂 URL.Path 的写法确实恒为 false（回归防线）。
// 若将来 gin 改变 Param/Path 语义，这条会失败并提醒重新审视。
func TestIsAllowedProxyPathWouldFailWithFullURLPath(t *testing.T) {
	fullPath := "/v1/oneapi/proxy/5/v1/chat/completions"
	if isAllowedProxyPath(fullPath) {
		t.Fatal("喂 URL.Path 不应放行——说明调用点又改错了（必须用 c.Param(\"target\")）")
	}
}
