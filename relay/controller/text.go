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
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/render"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
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

	// Stream probe: buffer until first content token, then replay + passthrough
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
				// Buffer until first content token, then replay + passthrough
				var buf bytes.Buffer
				scanner := bufio.NewScanner(probeResp.Body)
				scanner.Split(bufio.ScanLines)
				var probeUsage *model.Usage
				confirmed := false
				var responseSnippet string

				for scanner.Scan() {
					data := scanner.Text()

					if !confirmed {
						// Probe phase: write to buffer, check for first content
						buf.WriteString(data)
						buf.WriteString("\n")

						// Check for first meaningful content token
						if len(data) > 6 && data[:6] == "data: " {
							var streamResp openai.ChatCompletionsStreamResponse
							if err := json.Unmarshal([]byte(data[6:]), &streamResp); err == nil {
								// Content detected via delta.content or reasoning_content
								if len(streamResp.Choices) > 0 {
									delta := &streamResp.Choices[0].Delta
									if content, ok := delta.Content.(string); ok && content != "" {
										confirmed = true
									} else if rc, ok := delta.ReasoningContent.(string); ok && rc != "" {
										confirmed = true
									}
								}
								// Also capture usage whenever available
								if streamResp.Usage != nil {
									probeUsage = streamResp.Usage
								}
							}
						}

						if confirmed {
							// First content token found: set headers, replay buffer, then passthrough
							logger.Infof(ctx, "stream confirmed with first content token, replaying %d buffered bytes", buf.Len())

							// Write probe confirm log immediately for visibility
							chName := c.GetString(ctxkey.ChannelName)
							chId := c.GetInt(ctxkey.ChannelId)
							probeTTFT := helper.CalcElapsedTime(meta.StartTime)
							probeLogContent := getRequestPreview(textRequest)
							if probeLogContent != "" {
								probeLogContent = fmt.Sprintf("探测成功，请求模型：%s，请求内容：%s", meta.ActualModelName, probeLogContent)
							} else {
								probeLogContent = fmt.Sprintf("探测成功，请求模型：%s，%s | %dms", meta.ActualModelName, chName, probeTTFT)
							}
							dbmodel.RecordConsumeLog(ctx, &dbmodel.Log{
								UserId:            meta.UserId,
								TokenName:         meta.TokenName,
								ModelName:         meta.OriginModelName,
								ChannelId:         chId,
								PromptTokens:      promptTokens,
								CompletionTokens:  0,
								Quota:             0,
								Content:           probeLogContent,
								IsStream:          true,
								ElapsedTime:       probeTTFT,
								SystemPromptReset: systemPromptReset,
							})

							// 记录 TTFT 到滑动窗口（后续 postConsumeQuota 会记录完整 tok/s）
							monitor.GlobalPerformanceStore.RecordRequest(
								chId,
								promptTokens,
								0, // 完成 tokens 未知
								probeTTFT,
								probeTTFT, // TTFT = 从开始到首次 content 的耗时
							)

							common.SetEventStreamHeaders(c)

							bufReader := bytes.NewReader(buf.Bytes())
							lineScanner := bufio.NewScanner(bufReader)
							lineScanner.Split(bufio.ScanLines)
							for lineScanner.Scan() {
								line := lineScanner.Text()
								if len(line) > 0 {
									render.StringData(c, line)
								}
							}
							buf.Reset()
						}
					} else {
						// Passthrough mode: forward directly
						if len(data) > 0 {
							render.StringData(c, data)
						}

						// Accumulate response content snippet from passthrough
						if responseSnippet == "" && len(data) > 6 && data[:6] == "data: " {
							var streamResp openai.ChatCompletionsStreamResponse
							if json.Unmarshal([]byte(data[6:]), &streamResp) == nil && len(streamResp.Choices) > 0 {
								delta := &streamResp.Choices[0].Delta
								if c, ok := delta.Content.(string); ok && c != "" {
									runes := []rune(c)
									if len(runes) > 30 {
										responseSnippet = string(runes[:30]) + "…"
									} else {
										responseSnippet = c
									}
								}
							}
						}

						// Still extract usage from passthrough
						if len(data) > 6 && data[:6] == "data: " {
							var streamResp openai.ChatCompletionsStreamResponse
							if json.Unmarshal([]byte(data[6:]), &streamResp) == nil && streamResp.Usage != nil {
								probeUsage = streamResp.Usage
							}
						}
					}
				}
				probeResp.Body.Close()

				if !confirmed {
					// Stream ended without any content → empty response, trigger fallback
					logger.Warnf(ctx, "stream probe returned empty response (no content token found), triggering fallback")

					// Write probe failure log
					chName := c.GetString(ctxkey.ChannelName)
					chId := c.GetInt(ctxkey.ChannelId)
					probeFailLogContent := getRequestPreview(textRequest)
					if probeFailLogContent != "" {
						probeFailLogContent = fmt.Sprintf("探测失败，请求模型：%s，请求内容：%s", meta.ActualModelName, probeFailLogContent)
					} else {
						probeFailLogContent = fmt.Sprintf("探测失败，请求模型：%s，%s | 空响应", meta.ActualModelName, chName)
					}
					dbmodel.RecordConsumeLog(ctx, &dbmodel.Log{
						UserId:            meta.UserId,
						TokenName:         meta.TokenName,
						ModelName:         meta.OriginModelName,
						ChannelId:         chId,
						PromptTokens:      promptTokens,
						CompletionTokens:  0,
						Quota:             0,
						Content:           probeFailLogContent,
						IsStream:          true,
						ElapsedTime:       helper.CalcElapsedTime(meta.StartTime),
						SystemPromptReset: systemPromptReset,
					})

					billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
					return openai.ErrorWrapper(fmt.Errorf("empty response from channel during probe"), "empty_response", http.StatusBadGateway)
				}

				logger.Infof(ctx, "stream finished with passthrough, usage: %+v", probeUsage)
				render.Done(c)

				go postConsumeQuota(ctx, probeUsage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset, responseSnippet)
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
	go postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset, "")
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