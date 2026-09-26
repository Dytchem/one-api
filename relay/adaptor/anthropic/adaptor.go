package anthropic

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
)

type Adaptor struct {
}

func (a *Adaptor) Init(meta *meta.Meta) {

}

func (a *Adaptor) GetRequestURL(meta *meta.Meta) (string, error) {
	return fmt.Sprintf("%s/v1/messages", meta.BaseURL), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *meta.Meta) error {
	adaptor.SetupCommonRequestHeader(c, req, meta)
	req.Header.Set("x-api-key", meta.APIKey)
	anthropicVersion := c.Request.Header.Get("anthropic-version")
	if anthropicVersion == "" {
		anthropicVersion = "2023-06-01"
	}
	req.Header.Set("anthropic-version", anthropicVersion)

	// dyt-114: 透传客户端声明的 anthropic-beta。
	// 原实现把 anthropic-beta 硬编码成 messages-2023-12-15，导致客户端启用的
	// 扩展思考（interleaved-thinking）、上下文压缩（compact-*）、细粒度工具流
	// 等 beta 能力在网关处被静默丢弃——请求能通但新特性全部失效。
	// 这里改为：客户端声明的 beta 全部保留，网关再补上自己必需的基础 beta。
	betas := collectAnthropicBetas(c.Request.Header)
	// 网关自身依赖的基础 beta（Anthropic 要求显式声明才能用对应字段）
	betas = appendBetaIfMissing(betas, "messages-2023-12-15")

	// https://x.com/alexalbert__/status/1812921642143900036
	// claude-3-5-sonnet can support 8k context
	if strings.HasPrefix(meta.ActualModelName, "claude-3-5-sonnet") {
		betas = appendBetaIfMissing(betas, "max-tokens-3-5-sonnet-2024-07-15")
	}
	if len(betas) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}

	return nil
}

// collectAnthropicBetas 收集客户端声明的 beta 列表。
// Anthropic 允许 header 出现多次或逗号分隔，两种都要支持。
func collectAnthropicBetas(h http.Header) []string {
	var out []string
	for _, raw := range h.Values("anthropic-beta") {
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				out = appendBetaIfMissing(out, p)
			}
		}
	}
	return out
}

// appendBetaIfMissing 追加 beta（去重，保持顺序）
func appendBetaIfMissing(list []string, beta string) []string {
	for _, b := range list {
		if strings.EqualFold(b, beta) {
			return list
		}
	}
	return append(list, beta)
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return ConvertRequest(*request), nil
}

func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *meta.Meta, requestBody io.Reader) (*http.Response, error) {
	return adaptor.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	if meta.IsStream {
		err, usage = StreamHandler(c, resp)
	} else {
		err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return "anthropic"
}
