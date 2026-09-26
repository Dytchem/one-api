package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/config"
)

// dyt-109: gzip 解压炸弹回归测试。
//
// 本中间件挂在 router 级、先于 bodySizeLimit 执行，而 bodySizeLimit 的
// MaxBytesReader 包的是解压**后**的 body。原实现直接
// `io.NopCloser(gzipReader)`，于是极小的 gzip 可解出极大数据绕过体积上限。
func TestGzipDecodeBlocksDecompressionBomb(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 构造一个高度可压缩的巨大 payload（远大于 MAX_REQUEST_BODY_MB 默认 32MB）
	orig := config.MaxRequestBodyMB
	config.MaxRequestBodyMB = 1 // 测试用 1MB 上限，避免真的分配几百 MB
	defer func() { config.MaxRequestBodyMB = orig }()

	big := bytes.Repeat([]byte("A"), 8<<20) // 8MB 解压后内容
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(big); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	compressed := buf.Bytes()
	// 压缩后应远小于解压后（确认这确实是个"炸弹"场景）
	if len(compressed) >= len(big)/2 {
		t.Fatalf("构造的压缩体不够压缩: %d vs %d", len(compressed), len(big))
	}

	var readLen int
	var readErr error
	r := gin.New()
	r.Use(GzipDecodeMiddleware())
	r.POST("/t", func(c *gin.Context) {
		var b []byte
		b, readErr = io.ReadAll(c.Request.Body)
		readLen = len(b)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/t", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// 关键断言：读取必须因超限失败，而不是把 8MB 全读进来
	if readErr == nil {
		t.Fatalf("解压炸弹未被拦截：成功读取了 %d 字节（压缩体仅 %d 字节）", readLen, len(compressed))
	}
	if readLen > 2<<20 {
		t.Fatalf("读取量超过上限+冗余: %d 字节", readLen)
	}
	t.Logf("已拦截：压缩体 %d 字节 -> 读取在 %d 字节处失败 (%v)", len(compressed), readLen, readErr)
}

// 正常的小 gzip 请求必须照常工作（不能误伤）
func TestGzipDecodeStillWorksForNormalPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	_ = zw.Close()

	var got []byte
	var gotErr error
	r := gin.New()
	r.Use(GzipDecodeMiddleware())
	r.POST("/t", func(c *gin.Context) {
		got, gotErr = io.ReadAll(c.Request.Body)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/t", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if gotErr != nil {
		t.Fatalf("正常 gzip 请求不应失败: %v", gotErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("解压内容不一致\n got: %s\nwant: %s", got, payload)
	}
}

// 非 gzip 请求必须原样透传
func TestGzipDecodePassesThroughPlainBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"plain":true}`)
	var got []byte
	r := gin.New()
	r.Use(GzipDecodeMiddleware())
	r.POST("/t", func(c *gin.Context) {
		got, _ = io.ReadAll(c.Request.Body)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/t", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !bytes.Equal(got, payload) {
		t.Fatalf("非 gzip 请求体应原样透传\n got: %s\nwant: %s", got, payload)
	}
}
