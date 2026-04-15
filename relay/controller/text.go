package controller

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/render"
	"github.com/songquanpeng/one-api/relay"
	"github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/apitype"
	"github.com/songquanpeng/one-api/relay/billing"
	billingratio "github.com/songquanpeng/one-api/relay/billing/ratio"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
)

func RelayTextHelper(c *gin.Context) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)
	textRequest, err := getAndValidateTextRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getAndValidateTextRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}
	meta.IsStream = textRequest.Stream

	originModel := c.GetString(ctxkey.OriginalModel)
	if originModel != "" {
		meta.OriginModelName = originModel
		textRequest.Model = originModel
	} else {
		meta.OriginModelName = textRequest.Model
	}
	textRequest.Model, _ = getMappedModelName(textRequest.Model, meta.ModelMapping)
	meta.ActualModelName = textRequest.Model
	systemPromptReset := setSystemPrompt(ctx, textRequest, meta.ForcedSystemPrompt)
	modelRatio := billingratio.GetModelRatio(textRequest.Model, meta.ChannelType)
	groupRatio := billingratio.GetGroupRatio(meta.Group)
	ratio := modelRatio * groupRatio
	promptTokens := getPromptTokens(textRequest, meta.Mode)
	meta.PromptTokens = promptTokens
	preConsumedQuota, bizErr := preConsumeQuota(ctx, textRequest, promptTokens, ratio, meta)
	if bizErr != nil {
		logger.Warnf(ctx, "preConsumeQuota failed: %+v", *bizErr)
		return bizErr
	}

	adaptor := relay.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusInternalServerError)
	}
	adaptor.Init(meta)

	// For stream requests: probe with streaming, buffer data, then replay to client
	if meta.IsStream {
		// Create a clone for probe
		probeRequest := &model.GeneralOpenAIRequest{}
		probeBytes, _ := json.Marshal(textRequest)
		json.Unmarshal(probeBytes, probeRequest)
		// Use streaming for probe
		probeRequest.Stream = true

		// Get probe request body
		probeRequestBody, err := getRequestBody(c, meta, probeRequest, adaptor)
		if err == nil {
			// Do probe request with streaming
			probeResp, probeErr := adaptor.DoRequest(c, meta, probeRequestBody)
			if probeErr == nil && probeResp != nil && probeResp.StatusCode/100 == 2 {
				// Probe succeeded - buffer the stream data
				var probeBuffer bytes.Buffer
				scanner := bufio.NewScanner(probeResp.Body)
				scanner.Split(bufio.ScanLines)
				var probeUsage *model.Usage

				for scanner.Scan() {
					data := scanner.Text()
					probeBuffer.WriteString(data)
					probeBuffer.WriteString("\n")

					// Try to extract usage from the last line
					if len(data) > 6 && data[:6] == "data: " {
						var streamResp openai.ChatCompletionsStreamResponse
						if json.Unmarshal([]byte(data[6:]), &streamResp) == nil && streamResp.Usage != nil {
							probeUsage = streamResp.Usage
						}
					}
				}
				probeResp.Body.Close()

				// Check if probe has valid content
				if probeUsage == nil || probeUsage.CompletionTokens == 0 {
					// Probe returned empty - trigger fallback
					logger.Warnf(ctx, "stream probe returned empty response, triggering fallback")
					billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
					return openai.ErrorWrapper(fmt.Errorf("empty response from channel during probe"), "empty_response", http.StatusBadGateway)
				}

				logger.Infof(ctx, "stream probe successful with %d bytes buffered", probeBuffer.Len())

				// Now set headers and replay buffered data to client
				common.SetEventStreamHeaders(c)
				reader := bytes.NewReader(probeBuffer.Bytes())
				streamScanner := bufio.NewScanner(reader)
				streamScanner.Split(bufio.ScanLines)

				for streamScanner.Scan() {
					data := streamScanner.Text()
					if len(data) > 0 {
						render.StringData(c, data)
					}
				}
				render.Done(c)

				// Post consume and return - the probe response was sent to client
				go postConsumeQuota(ctx, probeUsage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset)
				return nil
			} else {
				logger.Warnf(ctx, "stream probe request failed: %v", probeErr)
			}
		} else {
			logger.Warnf(ctx, "probe request body failed: %s", err.Error())
		}
	}

	// Normal flow (non-stream or probe failed)
	requestBody, err := getRequestBody(c, meta, textRequest, adaptor)
	if err != nil {
		return openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
	}

	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if isErrorHappened(meta, resp) {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return RelayErrorHandler(resp)
	}

	usage, respErr := adaptor.DoResponse(c, resp, meta)
	if respErr != nil {
		logger.Errorf(ctx, "respErr is not nil: %+v", respErr)
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return respErr
	}
	if usage != nil && usage.CompletionTokens == 0 {
		logger.Warnf(ctx, "empty response detected (completion_tokens=0), triggering fallback")
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return openai.ErrorWrapper(fmt.Errorf("empty response from channel"), "empty_response", http.StatusBadGateway)
	}
	go postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset)
	return nil
}

func getRequestBody(c *gin.Context, meta *meta.Meta, textRequest *model.GeneralOpenAIRequest, adaptor adaptor.Adaptor) (io.Reader, error) {
	if !config.EnforceIncludeUsage &&
		meta.APIType == apitype.OpenAI &&
		meta.OriginModelName == meta.ActualModelName &&
		meta.ChannelType != channeltype.Baichuan &&
		meta.ForcedSystemPrompt == "" {
		return c.Request.Body, nil
	}

	var requestBody io.Reader
	convertedRequest, err := adaptor.ConvertRequest(c, meta.Mode, textRequest)
	if err != nil {
		logger.Debugf(c.Request.Context(), "converted request failed: %s\n", err.Error())
		return nil, err
	}
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		logger.Debugf(c.Request.Context(), "converted request json_ marshal_ failed: %s\n", err.Error())
		return nil, err
	}
	logger.Debugf(c.Request.Context(), "converted request: \n%s", string(jsonData))
	requestBody = bytes.NewBuffer(jsonData)
	return requestBody, nil
}