package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/logger"
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
	// get & validate textRequest
	textRequest, err := getAndValidateTextRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getAndValidateTextRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}
	meta.IsStream = textRequest.Stream

	// map model name
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

	// For stream requests: probe first with buffering
	if meta.IsStream {
		// Create a non-stream clone for probing
		probeRequest := &model.GeneralOpenAIRequest{}
		probeBytes, _ := json.Marshal(textRequest)
		json.Unmarshal(probeBytes, probeRequest)
		probeRequest.Stream = false

		// Get probe request body
		probeRequestBody, err := getRequestBody(c, meta, probeRequest, adaptor)
		if err != nil {
			logger.Warnf(ctx, "probe request body failed: %s", err.Error())
		} else {
			// Do probe request
			probeResp, probeErr := adaptor.DoRequest(c, meta, probeRequestBody)
			if probeErr != nil {
				logger.Warnf(ctx, "probe request failed: %s", probeErr.Error())
			} else if probeResp != nil && probeResp.StatusCode/100 == 2 {
				// Probe succeeded - validate has content
				probeUsage, probeRespErr := adaptor.DoResponse(c, probeResp, meta)
				if probeRespErr == nil && probeUsage != nil && probeUsage.CompletionTokens > 0 {
					// Probe successful with valid response
					logger.Infof(ctx, "stream probe successful, buffering and streaming")
					// Buffer the probe response body for replay
					var buffer bytes.Buffer
					io.Copy(&buffer, probeResp.Body)
					probeResp.Body.Close()

					// Now replay buffered data to client as stream
					reader := bytes.NewReader(buffer.Bytes())
					scanner := bufio.NewScanner(reader)
					scanner.Split(bufio.ScanLines)
					for scanner.Scan() {
						data := scanner.Text()
						if len(data) < dataPrefixLength {
							continue
						}
						dataWithoutPrefix := data[dataPrefixLength:]
						if dataWithoutPrefix == done || data[:dataPrefixLength] != dataPrefix {
							render.StringData(c, data)
							continue
						}
						switch relayMode {
						case relaymode.ChatCompletions:
							var streamResponse ChatCompletionsStreamResponse
							if json.Unmarshal([]byte(dataWithoutPrefix), &streamResponse) == nil {
								render.StringData(c, data)
								for _, choice := range streamResponse.Choices {
									responseText += conv.AsString(choice.Delta.Content)
								}
							} else {
								render.StringData(c, data)
							}
						case relaymode.Completions:
							var streamResponse CompletionsStreamResponse
							if json.Unmarshal([]byte(dataWithoutPrefix), &streamResponse) == nil {
								render.StringData(c, data)
								for _, choice := range streamResponse.Choices {
									responseText += choice.Text
								}
							} else {
								render.StringData(c, data)
							}
						}
					}
				} else {
					probeResp.Body.Close()
					if probeUsage != nil && probeUsage.CompletionTokens == 0 {
						billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
						return openai.ErrorWrapper(fmt.Errorf("empty response from channel during probe"), "empty_response", http.StatusBadGateway)
					}
					logger.Warnf(ctx, "probe incomplete, falling back")
				}
			} else {
				logger.Warnf(ctx, "probe request failed status: %d", probeResp.StatusCode)
			}
		}
	}

	// Normal request body for actual streaming
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

	// Stream the buffered + real response
	_, respErr := openai.StreamHandler(c, resp, meta.Mode)
	if respErr != nil {
		logger.Errorf(ctx, "respErr is not nil: %+v", respErr)
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return respErr
	}
	// check for empty response
	if respErr == nil && usage != nil && usage.CompletionTokens == 0 {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return openai.ErrorWrapper(fmt.Errorf("empty response from channel"), "empty_response", http.StatusBadGateway)
	}
	go postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset)
	return nil
}