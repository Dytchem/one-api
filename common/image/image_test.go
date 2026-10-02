package image_test

import (
	"encoding/base64"
	"github.com/songquanpeng/one-api/common/client"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	img "github.com/songquanpeng/one-api/common/image"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "golang.org/x/image/webp"
)

type CountingReader struct {
	reader    io.Reader
	BytesRead int
}

func (r *CountingReader) Read(p []byte) (n int, err error) {
	n, err = r.reader.Read(p)
	r.BytesRead += n
	return n, err
}

var (
	cases = []struct {
		url    string
		format string
		width  int
		height int
	}{
		// dyt-121: 原先第一条 fixture 是 wikimedia 的 thumb 缩略图 URL
		// (…/thumb/…/2560px-….jpg)，该文件已从 Commons 下架（现在一律 400；
		// 且 wikimedia 对本网络的所有 /thumb/ URL 都返回 400），导致
		// TestGetImageSize 永远有一条红的、掩盖真实回归。改为只保留
		// 仍可直达的原图 URL，格式覆盖不变（png/webp/gif/jpeg）。
		{"https://upload.wikimedia.org/wikipedia/commons/9/97/Basshunter_live_performances.png", "png", 4500, 2592},
		{"https://upload.wikimedia.org/wikipedia/commons/c/c6/TO_THE_ONE_SOMETHINGNESS.webp", "webp", 984, 985},
		{"https://upload.wikimedia.org/wikipedia/commons/d/d0/01_Das_Sandberg-Modell.gif", "gif", 1917, 1533},
		{"https://upload.wikimedia.org/wikipedia/commons/6/62/102Cervus.jpg", "jpeg", 270, 230},
	}
)

// dyt-121: 这些用例会下载 wikimedia 上的真实图片。离线/被墙时 http.Get 失败，
// 而原来的 assert.NoError 不会中断执行 —— 紧接着就对 nil 解引用 panic，
// 使 `go test ./...` 永远变红并掩盖真实回归。
// 网络不可用应当跳过（用例本身验证的是解码，不是网络），解码失败则用 require
// 立即终止，避免 nil 解引用。
func fetchFixture(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil || resp == nil {
		t.Skipf("跳过：测试图片不可下载（网络不可用）: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Skipf("跳过：测试图片返回 HTTP %d", resp.StatusCode)
	}
	return resp
}

func TestMain(m *testing.M) {
	client.Init()
	m.Run()
}

func TestDecode(t *testing.T) {
	// Bytes read: varies sometimes
	// jpeg: 1063892
	// png: 294462
	// webp: 99529
	// gif: 956153
	// jpeg#01: 32805
	for _, c := range cases {
		t.Run("Decode:"+c.format, func(t *testing.T) {
			resp := fetchFixture(t, c.url)
			defer resp.Body.Close()
			reader := &CountingReader{reader: resp.Body}
			img, format, err := image.Decode(reader)
			require.NoError(t, err)
			size := img.Bounds().Size()
			assert.Equal(t, c.format, format)
			assert.Equal(t, c.width, size.X)
			assert.Equal(t, c.height, size.Y)
			t.Logf("Bytes read: %d", reader.BytesRead)
		})
	}

	// Bytes read:
	// jpeg: 4096
	// png: 4096
	// webp: 4096
	// gif: 4096
	// jpeg#01: 4096
	for _, c := range cases {
		t.Run("DecodeConfig:"+c.format, func(t *testing.T) {
			resp := fetchFixture(t, c.url)
			defer resp.Body.Close()
			reader := &CountingReader{reader: resp.Body}
			config, format, err := image.DecodeConfig(reader)
			require.NoError(t, err)
			assert.Equal(t, c.format, format)
			assert.Equal(t, c.width, config.Width)
			assert.Equal(t, c.height, config.Height)
			t.Logf("Bytes read: %d", reader.BytesRead)
		})
	}
}

func TestBase64(t *testing.T) {
	// Bytes read:
	// jpeg: 1063892
	// png: 294462
	// webp: 99072
	// gif: 953856
	// jpeg#01: 32805
	for _, c := range cases {
		t.Run("Decode:"+c.format, func(t *testing.T) {
			resp := fetchFixture(t, c.url)
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			assert.NoError(t, err)
			encoded := base64.StdEncoding.EncodeToString(data)
			body := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
			reader := &CountingReader{reader: body}
			img, format, err := image.Decode(reader)
			require.NoError(t, err)
			size := img.Bounds().Size()
			assert.Equal(t, c.format, format)
			assert.Equal(t, c.width, size.X)
			assert.Equal(t, c.height, size.Y)
			t.Logf("Bytes read: %d", reader.BytesRead)
		})
	}

	// Bytes read:
	// jpeg: 1536
	// png: 768
	// webp: 768
	// gif: 1536
	// jpeg#01: 3840
	for _, c := range cases {
		t.Run("DecodeConfig:"+c.format, func(t *testing.T) {
			resp := fetchFixture(t, c.url)
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			assert.NoError(t, err)
			encoded := base64.StdEncoding.EncodeToString(data)
			body := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
			reader := &CountingReader{reader: body}
			config, format, err := image.DecodeConfig(reader)
			require.NoError(t, err)
			assert.Equal(t, c.format, format)
			assert.Equal(t, c.width, config.Width)
			assert.Equal(t, c.height, config.Height)
			t.Logf("Bytes read: %d", reader.BytesRead)
		})
	}
}

func TestGetImageSize(t *testing.T) {
	for i, c := range cases {
		t.Run("Decode:"+strconv.Itoa(i), func(t *testing.T) {
			width, height, err := img.GetImageSize(c.url)
			assert.NoError(t, err)
			assert.Equal(t, c.width, width)
			assert.Equal(t, c.height, height)
		})
	}
}

func TestGetImageSizeFromBase64(t *testing.T) {
	for i, c := range cases {
		t.Run("Decode:"+strconv.Itoa(i), func(t *testing.T) {
			resp := fetchFixture(t, c.url)
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			assert.NoError(t, err)
			encoded := base64.StdEncoding.EncodeToString(data)
			width, height, err := img.GetImageSizeFromBase64(encoded)
			assert.NoError(t, err)
			assert.Equal(t, c.width, width)
			assert.Equal(t, c.height, height)
		})
	}
}
