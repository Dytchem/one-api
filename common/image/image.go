package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/songquanpeng/one-api/common/client"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

// Regex to match data URL pattern
var dataURLPattern = regexp.MustCompile(`data:image/([^;]+);base64,(.*)`)

// dyt-96: 用户提交的 image_url 由服务端抓取（计 token / 转发），
// 必须视为不可信输入：限时、限体、禁重定向、阻断私网/回环/链路本地地址（SSRF 防护）。
const (
	imageFetchTimeout  = 5 * time.Second
	imageFetchMaxBytes = 4 << 20 // 4MB
)

// fetchClient: 若配置了 USER_CONTENT_REQUEST_PROXY 则复用其代理 Transport，否则用受控默认 client
func fetchClient() *http.Client {
	if client.UserContentRequestHTTPClient != nil && client.UserContentRequestHTTPClient.Transport != nil {
		return &http.Client{
			Timeout:   imageFetchTimeout,
			Transport: client.UserContentRequestHTTPClient.Transport,
			// 拒绝跟随重定向（防止跳转到内网/元数据地址后再探测）
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return imageFetchClient
}

var imageFetchClient = &http.Client{
	Timeout: imageFetchTimeout,
	// 拒绝跟随重定向（防止跳转到内网/元数据地址后再探测）
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// isBlockedTarget: 解析 URL host，若解析失败或指向回环/私网/链路本地/多播地址则拒绝
func isBlockedTarget(rawURL string) (bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return false, fmt.Errorf("invalid url: %s", rawURL)
	}
	ips, err := net.LookupIP(parsed.Hostname())
	if err != nil {
		// 解析失败不直接放行：交由请求层报错
		return false, err
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return true, nil
		}
	}
	return false, nil
}

// doImageRequest: 统一受控请求（SSRF 阻断 + 超时 + 禁重定向 + 响应体限流）
func doImageRequest(method, rawURL string) (*http.Response, error) {
	blocked, err := isBlockedTarget(rawURL)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, fmt.Errorf("image url 指向内网/回环地址，已拒绝")
	}
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return fetchClient().Do(req)
}

func IsImageUrl(url string) (bool, error) {
	resp, err := doImageRequest(http.MethodHead, url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		return false, nil
	}
	return true, nil
}

func GetImageSizeFromUrl(url string) (width int, height int, err error) {
	isImage, err := IsImageUrl(url)
	if !isImage {
		return
	}
	resp, err := doImageRequest(http.MethodGet, url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	img, _, err := image.DecodeConfig(io.LimitReader(resp.Body, imageFetchMaxBytes))
	if err != nil {
		return
	}
	return img.Width, img.Height, nil
}

func GetImageFromUrl(url string) (mimeType string, data string, err error) {
	// Check if the URL is a data URL
	matches := dataURLPattern.FindStringSubmatch(url)
	if len(matches) == 3 {
		// URL is a data URL
		mimeType = "image/" + matches[1]
		data = matches[2]
		return
	}

	isImage, err := IsImageUrl(url)
	if !isImage {
		return
	}
	resp, err := doImageRequest(http.MethodGet, url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	buffer := bytes.NewBuffer(nil)
	_, err = buffer.ReadFrom(io.LimitReader(resp.Body, imageFetchMaxBytes))
	if err != nil {
		return
	}
	mimeType = resp.Header.Get("Content-Type")
	data = base64.StdEncoding.EncodeToString(buffer.Bytes())
	return
}

var (
	reg = regexp.MustCompile(`data:image/([^;]+);base64,`)
)

var readerPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Reader{}
	},
}

func GetImageSizeFromBase64(encoded string) (width int, height int, err error) {
	decoded, err := base64.StdEncoding.DecodeString(reg.ReplaceAllString(encoded, ""))
	if err != nil {
		return 0, 0, err
	}

	reader := readerPool.Get().(*bytes.Reader)
	defer readerPool.Put(reader)
	reader.Reset(decoded)

	img, _, err := image.DecodeConfig(reader)
	if err != nil {
		return 0, 0, err
	}

	return img.Width, img.Height, nil
}

func GetImageSize(image string) (width int, height int, err error) {
	if strings.HasPrefix(image, "data:image/") {
		return GetImageSizeFromBase64(image)
	}
	return GetImageSizeFromUrl(image)
}
