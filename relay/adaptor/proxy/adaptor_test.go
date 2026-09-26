package proxy

import (
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/relay/meta"
)

// dyt-106: proxy 适配器的路径逃逸回归测试。
//
// 背景：普通用户的 proxy 目标白名单用 HasPrefix 判定，而 Go/gin 不做路径清理。
// `/v1/oneapi/proxy/5/v1/images/../../../../admin/setting` 会因前缀
// `/v1/images` 通过白名单，随后适配器把原样路径拼到 BaseURL 上，
// 让非管理员触达上游任意路径。
func TestGetRequestURLBlocksTraversal(t *testing.T) {
	a := &Adaptor{}
	base := "https://upstream.example.com"

	cases := []struct {
		name      string
		reqPath   string
		wantClean string // 期望最终 URL（空表示应报错）
	}{
		{
			name:      "正常 chat 路径不受影响",
			reqPath:   "/v1/oneapi/proxy/5/v1/chat/completions",
			wantClean: base + "/v1/chat/completions",
		},
		{
			name:      "正常 models 路径",
			reqPath:   "/v1/oneapi/proxy/5/v1/models",
			wantClean: base + "/v1/models",
		},
		{
			name:      "images 下的正常路径",
			reqPath:   "/v1/oneapi/proxy/5/v1/images/generations",
			wantClean: base + "/v1/images/generations",
		},
		{
			name:      "`..` 逃逸必须被拒绝",
			reqPath:   "/v1/oneapi/proxy/5/v1/images/../../../../admin/setting",
			wantClean: "__REJECT__",
		},
		{
			name:      "逃出 /v1 命名空间的路径必须被拒绝",
			reqPath:   "/v1/oneapi/proxy/5/api/channel",
			wantClean: "__REJECT__",
		},
		{
			name:      "根路径必须被拒绝",
			reqPath:   "/v1/oneapi/proxy/5/",
			wantClean: "__REJECT__",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &meta.Meta{
				ChannelId:      5,
				BaseURL:        base,
				RequestURLPath: tc.reqPath,
			}
			got, err := a.GetRequestURL(m)
			if tc.wantClean == "__REJECT__" {
				if err == nil {
					t.Fatalf("越权路径应被拒绝，却返回: %s", got)
				}
				t.Logf("已拒绝: %v", err)
				return
			}
			if err != nil {
				t.Fatalf("正常路径不应报错: %v", err)
			}
			if got != tc.wantClean {
				t.Fatalf("URL 不匹配\n got: %s\nwant: %s", got, tc.wantClean)
			}
		})
	}
}

// query 应被保留
func TestGetRequestURLKeepsQuery(t *testing.T) {
	a := &Adaptor{}
	m := &meta.Meta{
		ChannelId:      5,
		BaseURL:        "https://upstream.example.com",
		RequestURLPath: "/v1/oneapi/proxy/5/v1/models?limit=10",
	}
	got, err := a.GetRequestURL(m)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !strings.HasSuffix(got, "/v1/models?limit=10") {
		t.Fatalf("query 丢失: %s", got)
	}
}
