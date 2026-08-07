package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// dyt-104: proxy 目标路径白名单（非 admin SSRF 收敛）
func TestIsAllowedProxyPath(t *testing.T) {
	allowed := []string{
		"/v1/chat/completions",
		"/v1/chat/completions/sub",
		"/v1/completions",
		"/v1/embeddings",
		"/v1/images",
		"/v1/images/generations",
		"/v1/audio/speech",
		"/v1/models",
		"/v1/moderations",
	}
	for _, p := range allowed {
		if !isAllowedProxyPath(p) {
			t.Errorf("expected %q to be allowed", p)
		}
	}

	denied := []string{
		"",
		"/",
		"/admin",
		"/v1/chat/completionsX", // 前缀绕过：非路径段边界
		"/v1/oneapi/proxy/3/v1/chat/completions",
		"/v2/chat/completions",
		"/v1/files",
	}
	for _, p := range denied {
		if isAllowedProxyPath(p) {
			t.Errorf("expected %q to be denied", p)
		}
	}
}

// dyt-104: 渠道分组放行规则
func TestChannelGroupAllowed(t *testing.T) {
	cases := []struct {
		channelGroup string
		userGroup    string
		want         bool
	}{
		{"", "default", true}, // 渠道未配置分组 = 全部放行
		{"default", "default", true},
		{"default,vip", "vip", true},
		{"default, vip", "vip", true},
		{"vip", "default", false},
	}
	for _, tc := range cases {
		if got := channelGroupAllowed(tc.channelGroup, tc.userGroup); got != tc.want {
			t.Errorf("channelGroupAllowed(%q, %q) = %v, want %v",
				tc.channelGroup, tc.userGroup, got, tc.want)
		}
	}
}

// dyt-104: 同源守卫（GET 状态变更接口的 CSRF 防护）
func TestSameOriginGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/bind", SameOriginGuard(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	cases := []struct {
		name    string
		origin  string
		referer string
		want    int
	}{
		{"same origin header", "https://oneapi.local", "", http.StatusOK},
		{"same host referer", "", "https://oneapi.local/settings", http.StatusOK},
		{"cross site origin", "https://evil.example", "", http.StatusForbidden},
		{"cross site referer", "", "https://evil.example/page", http.StatusForbidden},
		{"both missing", "", "", http.StatusForbidden},
		{"origin cross site with referer ok", "https://evil.example", "https://oneapi.local/x", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://oneapi.local/bind", nil)
			req.Host = "oneapi.local"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("got status %d, want %d", w.Code, tc.want)
			}
		})
	}
}
