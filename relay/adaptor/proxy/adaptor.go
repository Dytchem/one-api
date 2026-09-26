package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/songquanpeng/one-api/relay/adaptor"
	channelhelper "github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

var _ adaptor.Adaptor = new(Adaptor)

const channelName = "proxy"

type Adaptor struct{}

func (a *Adaptor) Init(meta *meta.Meta) {
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("notimplement")
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	for k, v := range resp.Header {
		for _, vv := range v {
			c.Writer.Header().Set(k, vv)
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)
	if _, gerr := io.Copy(c.Writer, resp.Body); gerr != nil {
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error: relaymodel.Error{
				Message: gerr.Error(),
			},
		}
	}

	return nil, nil
}

func (a *Adaptor) GetModelList() (models []string) {
	return nil
}

func (a *Adaptor) GetChannelName() string {
	return channelName
}

// GetRequestURL remove static prefix, and return the real request url to the upstream service
func (a *Adaptor) GetRequestURL(meta *meta.Meta) (string, error) {
	prefix := fmt.Sprintf("/v1/oneapi/proxy/%d", meta.ChannelId)
	target := strings.TrimPrefix(meta.RequestURLPath, prefix)
	// dyt-106: 归一化路径并强制约束在 /v1/ 命名空间内，阻断 `..` 越权。
	// Go/gin 不做路径清理，`/v1/oneapi/proxy/5/v1/images/../../../../admin/setting`
	// 会原样保留；普通用户的 proxy 目标白名单用的是 HasPrefix("/v1/images")，
	// 于是前缀匹配通过、`..` 却把请求带到上游任意路径（越权/SSRF 放大）。
	// 注意：仅 Clean 不够——Clean 会把它变成上游的 /admin/setting（仍是越权），
	// 所以这里额外要求清理后的路径必须以 /v1/ 开头且不含残余的 `..`。
	u, err := url.Parse(target)
	if err != nil {
		return "", errors.New("invalid proxy target path")
	}
	cleaned := path.Clean(u.Path)
	if cleaned == "." || cleaned == "/" {
		return "", errors.New("invalid proxy target path")
	}
	if !strings.HasPrefix(cleaned, "/v1/") {
		return "", errors.New("proxy target path must stay within /v1/")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", errors.New("invalid proxy target path")
	}
	target = cleaned
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return meta.BaseURL + target, nil

}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *meta.Meta) error {
	// dyt-96: 只透传标准内容类 header（Authorization 等鉴权头一律由渠道密钥覆盖，
	// 防止用户自定义 header 攻击上游/绕过网关鉴权）
	allowlist := []string{
		"Content-Type",
		"Accept",
		"Accept-Language",
		"User-Agent",
		"X-Request-Id",
	}
	for k := range c.Request.Header {
		for _, allow := range allowlist {
			if strings.EqualFold(k, allow) {
				req.Header.Set(k, c.Request.Header.Get(k))
				break
			}
		}
	}

	// remove unnecessary headers
	req.Header.Del("Host")
	req.Header.Del("Content-Length")
	req.Header.Del("Accept-Encoding")
	req.Header.Del("Connection")

	// set authorization header
	req.Header.Set("Authorization", meta.APIKey)

	return nil
}

func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	return nil, errors.Errorf("not implement")
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *meta.Meta, requestBody io.Reader) (*http.Response, error) {
	return channelhelper.DoRequestHelper(a, c, meta, requestBody)
}
