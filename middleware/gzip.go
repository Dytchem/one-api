package middleware

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/config"
)

// errBodyTooLarge 表示解压后的请求体超过上限。
var errBodyTooLarge = errors.New("request body too large after gzip decompression")

// limitedReadCloser 限制解压流的读取总量，超过 max 字节即返回 errBodyTooLarge。
// 返回错误而非静默截断：截断会得到半个 JSON，报错信息更具误导性。
type limitedReadCloser struct {
	r   io.Reader
	c   io.Closer
	max int64
	n   int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return n, errBodyTooLarge
	}
	return n, err
}

func (l *limitedReadCloser) Close() error { return l.c.Close() }

func GzipDecodeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Content-Encoding") == "gzip" {
			gzipReader, err := gzip.NewReader(c.Request.Body)
			if err != nil {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			defer gzipReader.Close()

			// Replace the request body with the decompressed data
			//
			// dyt-109: 解压流必须限长。本中间件挂在 router 级、先于 bodySizeLimit 执行，
			// 而 bodySizeLimit 的 MaxBytesReader 包的是**解压后**的 body ——
			// 于是压缩炸弹（极小的 gzip 解出极大内容）能绕过体积上限并在 ReadAll 时打爆内存。
			// 这里对解压流直接设上限（同样取 MaxRequestBodyMB，与未压缩路径一致）。
			maxBytes := int64(config.MaxRequestBodyMB) * 1024 * 1024
			if maxBytes > 0 {
				c.Request.Body = &limitedReadCloser{
					r:   io.LimitReader(gzipReader, maxBytes+1), // +1 用于探测"是否超限"
					c:   gzipReader,
					max: maxBytes,
				}
			} else {
				c.Request.Body = io.NopCloser(gzipReader)
			}
		}

		// Continue processing the request
		c.Next()
	}
}
