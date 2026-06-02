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

	// dyt-20: 缓存请求 JSON 副本，供 payload 存储
	requestJSON, _ := json.Marshal(textRequest)
	c.Set("dyt20_request_json", string(requestJSON))

	originModel := c.GetString(ctxkey.OriginalModel)
	if originModel != "" {
		meta.OriginModelName = originModel
		textRequest.Model = originModel
	} else {
		meta.OriginModelName = textRequest.Model
	}
	textRequest.Model, _ = getMappedModelName(textRequest.Model, meta.ModelMapping)
	meta.ActualModelName = textRequest.Model
	c.Set(ctxkey.ActualModel, meta.ActualModelName) // dyt-22: 供 relay.go 使用
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
		// dyt-23: 探测不再克隆请求，直接用 textRequest 的 JSON 作为 body
		// 这样两次 doProbe 发送完全相同的 body 给上游
		textRequest.Stream = true // 探测总是流式

		// 序列化 textRequest 作为 body（保持与原请求字段完全一致）
		probeBodyBytes, _ := json.Marshal(textRequest)

		// 重置 c.Request.Body 让后续 getRequestBody 调用走原 body 路径
		c.Request.Body = io.NopCloser(bytes.NewBuffer(probeBodyBytes))

		// dyt-23: 每次 doProbe 从同一份 bytes 创建新 reader，保证 body 完全相同
		if true {
			// 改进2：探测 empty response 时同渠道重试 1 次
			// 抽成闭包便于复用
			doProbe := func(retryLabel string) (success bool, probeUsage *model.Usage, responseSnippet string, buf *bytes.Buffer, scanner *bufio.Scanner, statusCode int, errReason string, respBody string) {
				// dyt-23: 每次重试都从原 bytes 重新创建 reader，确保 body 完全一致
				resp, doErr := adaptor.DoRequest(c, meta, bytes.NewReader(probeBodyBytes))
				if doErr != nil || resp == nil || resp.StatusCode/100 != 2 {
					code := 0
					if resp != nil {
						code = resp.StatusCode
					}
					if doErr != nil {
						return false, nil, "", nil, nil, code, "请求错误: " + doErr.Error(), ""
					}
					return false, nil, "", nil, nil, code, fmt.Sprintf("HTTP %d 非 2xx", code), ""
				}
				defer resp.Body.Close()

				var localBuf bytes.Buffer
				localBuf.Grow(4096)
				localScanner := bufio.NewScanner(resp.Body)
				localScanner.Split(bufio.ScanLines)
				var localUsage *model.Usage
				localConfirmed := false
				var localSnippet string

				// 改进1：追踪上游返回细节
				lineCount := 0
				bytesRead := 0
				sawDone := false
				lastLine := ""

				// dyt-20: 异常检测 — finish_reason 出现但缺 usage
				sawFinishReason := false
				finishReasonValue := ""

				// dyt-20: 完整响应 body 累积（限 100KB）
				var respBodyBuf bytes.Buffer
				const maxRespBodySize = 100 * 1024
				respBodyTruncated := false

				for localScanner.Scan() {
					data := localScanner.Text()
					lineCount++
					bytesRead += len(data)
					if len(data) > 0 {
						lastLine = data
					}

					// dyt-20: 累积响应 body
					if !respBodyTruncated {
						if respBodyBuf.Len()+len(data)+1 > maxRespBodySize {
							respBodyTruncated = true
							// 写一个标记
							respBodyBuf.WriteString("\n...[truncated at 100KB]")
						} else {
							respBodyBuf.WriteString(data)
							respBodyBuf.WriteString("\n")
						}
					}

					if !localConfirmed {
						// Probe phase: write to buffer, check for first content
						localBuf.WriteString(data)
						localBuf.WriteString("\n")

						// Check for first meaningful content token
						if len(data) > 6 && data[:6] == "data: " {
							// 检测 [DONE] 哨兵
							if data == "data: [DONE]" || data == "data:[DONE]" {
								sawDone = true
							} else {
								var streamResp openai.ChatCompletionsStreamResponse
								if jsonErr := json.Unmarshal([]byte(data[6:]), &streamResp); jsonErr == nil {
									// Content detected via delta.content or reasoning_content
									if len(streamResp.Choices) > 0 {
										delta := &streamResp.Choices[0].Delta
										if content, ok := delta.Content.(string); ok && content != "" {
											localConfirmed = true
										} else if rc, ok := delta.ReasoningContent.(string); ok && rc != "" {
											localConfirmed = true
										}
										// dyt-20: 记录 finish_reason 出现
										if fr := streamResp.Choices[0].FinishReason; fr != nil && *fr != "" {
											sawFinishReason = true
											finishReasonValue = *fr
										}
									}
									// Also capture usage whenever available
									if streamResp.Usage != nil {
										localUsage = streamResp.Usage
									}
								}
							}
						}

						if localConfirmed {
							// First content token found: set headers, replay buffer, then passthrough
							logger.Infof(ctx, "stream confirmed with first content token (retry=%s), replaying %d buffered bytes", retryLabel, localBuf.Len())

							// Write probe confirm log immediately for visibility
							chName := c.GetString(ctxkey.ChannelName)
							chId := c.GetInt(ctxkey.ChannelId)
							probeTTFT := helper.CalcElapsedTime(meta.StartTime)
							probeLogContent := getRequestPreview(textRequest)
							if probeLogContent != "" {
								probeLogContent = fmt.Sprintf("探测成功，渠道：%s(#%d)，模型：%s→%s，请求内容：%s", chName, chId, meta.OriginModelName, meta.ActualModelName, probeLogContent)
							} else {
								probeLogContent = fmt.Sprintf("探测成功，渠道：%s(#%d)，模型：%s→%s，%s | %dms", chName, chId, meta.OriginModelName, meta.ActualModelName, chName, probeTTFT)
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

							bufReader := bytes.NewReader(localBuf.Bytes())
							lineScanner := bufio.NewScanner(bufReader)
							lineScanner.Split(bufio.ScanLines)
							for lineScanner.Scan() {
								line := lineScanner.Text()
								if len(line) > 0 {
									render.StringData(c, line)
								}
							}
							localBuf.Reset()
						}
					} else {
						// Passthrough mode: forward directly
						if len(data) > 0 {
							render.StringData(c, data)
						}

						// Accumulate response content snippet from passthrough
						if localSnippet == "" && len(data) > 6 && data[:6] == "data: " && data != "data: [DONE]" {
							var streamResp openai.ChatCompletionsStreamResponse
							if json.Unmarshal([]byte(data[6:]), &streamResp) == nil && len(streamResp.Choices) > 0 {
								delta := &streamResp.Choices[0].Delta
								if c, ok := delta.Content.(string); ok && c != "" {
									runes := []rune(c)
									if len(runes) > 30 {
										localSnippet = string(runes[:30]) + "…"
									} else {
										localSnippet = c
									}
								}
							}
						}

						// Still extract usage from passthrough
						if len(data) > 6 && data[:6] == "data: " && data != "data: [DONE]" {
							var streamResp openai.ChatCompletionsStreamResponse
							if json.Unmarshal([]byte(data[6:]), &streamResp) == nil && streamResp.Usage != nil {
								localUsage = streamResp.Usage
							}
						}
					}
				}

				// dyt-20: 失败判定。考虑 3 种情况：
				// 1) 未收到 content/reasoning token → empty response
				// 2) 收到 finish_reason 但缺 usage 且流末尾不是 [DONE] → 异常断流
				// 3) 正常空流（仅 [DONE]）→ 成功
				streamAbnormal := sawFinishReason && localUsage == nil && !sawDone
				if !localConfirmed || streamAbnormal {
					// Stream ended without content or with abnormal truncation
					reason := classifyEmptyResponse(lineCount, bytesRead, sawDone, lastLine, resp.StatusCode)
					// dyt-20: 附加 finish_reason 异常提示
					if streamAbnormal {
						reason = fmt.Sprintf("%s | 异常：finish_reason=%s 出现但缺 usage（疑似流中断）", reason, finishReasonValue)
					}
					return false, nil, "", nil, nil, resp.StatusCode, reason, respBodyBuf.String()
				}

				return true, localUsage, localSnippet, &localBuf, localScanner, resp.StatusCode, "", respBodyBuf.String()
			}

			// 改进2：第一次探测
			success, probeUsage, responseSnippet, _, _, _, errReason, _ := doProbe("first")
			if !success {
				logger.Warnf(ctx, "stream probe returned empty response on first try: %s, retrying once on same channel", errReason)

				// 第二次（重试）
				success, probeUsage2, responseSnippet2, _, _, _, errReason2, _ := doProbe("retry-1")
				if !success {
					// 两次都空 → 记录失败日志，触发 fallback
					combinedReason := errReason
					if errReason2 != "" && errReason2 != errReason {
						combinedReason = errReason + "；重试1次仍空: " + errReason2
					} else if errReason2 != "" {
						combinedReason = errReason + "；重试1次仍空"
					}

					logger.Warnf(ctx, "stream probe returned empty response after retry: %s, triggering fallback", combinedReason)

					chName := c.GetString(ctxkey.ChannelName)
					chId := c.GetInt(ctxkey.ChannelId)
					probeFailLogContent := getRequestPreview(textRequest)
					if probeFailLogContent != "" {
						probeFailLogContent = fmt.Sprintf("探测失败，渠道：%s(#%d)，模型：%s→%s，请求内容：%s | 上游：%s", chName, chId, meta.OriginModelName, meta.ActualModelName, probeFailLogContent, combinedReason)
					} else {
						probeFailLogContent = fmt.Sprintf("探测失败，渠道：%s(#%d)，模型：%s→%s，%s | 空响应 | 上游：%s", chName, chId, meta.OriginModelName, meta.ActualModelName, chName, combinedReason)
					}
					failLogId := dbmodel.RecordConsumeLogWithId(ctx, &dbmodel.Log{
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

					// dyt-20: 异步写 payload（包含原始请求/响应/错误）
					if failLogId > 0 {
						if reqJSON, ok := c.Get("dyt20_request_json"); ok {
							reqStr, _ := reqJSON.(string)
							dbmodel.RecordLogPayloadAsync(&dbmodel.LogPayload{
								LogId:     failLogId,
								Request:   reqStr,
								Response:  "", // 探测阶段响应被丢掉，保留空
								Error:     combinedReason,
								CreatedAt: meta.StartTime.Unix(),
							})
						}
					}

					billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
					return openai.ErrorWrapper(fmt.Errorf("empty response from channel during probe: %s", combinedReason), "empty_response", http.StatusBadGateway)
				}

				logger.Infof(ctx, "stream probe succeeded on retry-1 after first empty response")
				probeUsage = probeUsage2
				responseSnippet = responseSnippet2
			}

			logger.Infof(ctx, "stream finished with passthrough, usage: %+v", probeUsage)
			render.Done(c)

			go postConsumeQuota(ctx, probeUsage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset, responseSnippet)
			return nil
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
	// 提取非流式回复内容用于日志
	responseSnippet := ""
	if rt, ok := c.Get("response_content"); ok {
		responseSnippet, _ = rt.(string)
	}
	if usage != nil && usage.CompletionTokens == 0 {
		logger.Warnf(ctx, "empty response detected (completion_tokens=0), triggering fallback")
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		// dyt-20: 记录非流式空响应日志 + payload
		chName := c.GetString(ctxkey.ChannelName)
		chId := c.GetInt(ctxkey.ChannelId)
		failLogContent := fmt.Sprintf("回复为空，渠道：%s(#%d)，模型：%s→%s，completion_tokens=0", chName, chId, meta.OriginModelName, meta.ActualModelName)
		failLogId := dbmodel.RecordConsumeLogWithId(ctx, &dbmodel.Log{
			UserId:            meta.UserId,
			TokenName:         meta.TokenName,
			ModelName:         meta.OriginModelName,
			ChannelId:         chId,
			PromptTokens:      promptTokens,
			CompletionTokens:  0,
			Quota:             0,
			Content:           failLogContent,
			IsStream:          meta.IsStream,
			ElapsedTime:       helper.CalcElapsedTime(meta.StartTime),
			SystemPromptReset: systemPromptReset,
		})
		if failLogId > 0 {
			if reqJSON, ok := c.Get("dyt20_request_json"); ok {
				reqStr, _ := reqJSON.(string)
				dbmodel.RecordLogPayloadAsync(&dbmodel.LogPayload{
					LogId:     failLogId,
					Request:   reqStr,
					Response:  responseSnippet,
					Error:     "empty response (non-stream): completion_tokens=0",
					CreatedAt: meta.StartTime.Unix(),
				})
			}
		}
		return openai.ErrorWrapper(fmt.Errorf("empty response from channel"), "empty_response", http.StatusBadGateway)
	}
	go postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset, responseSnippet)
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

// extractStatusCode 改进B：从上游响应的 lastLine JSON 中提取 status_code，用于日志徽章
// 例如 lastLine = `{"choices":null,"usage":null,"base_resp":{"status_code":2013,"status_msg":"xxx"}}`
func extractStatusCode(lastLine string) (code int, msg string) {
	if lastLine == "" {
		return 0, ""
	}
	// 尝试解析 base_resp.status_code
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(lastLine), &raw) != nil {
		return 0, ""
	}
	baseResp, ok := raw["base_resp"]
	if !ok {
		return 0, ""
	}
	var br struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	}
	if json.Unmarshal(baseResp, &br) != nil {
		return 0, ""
	}
	return br.StatusCode, br.StatusMsg
}

// classifyEmptyResponse 改进AB：根据上游响应特征生成可读原因
// B: 附加 base_resp.status_code 徽章（如 [2013]）；A: 末行预览放宽到 200 字节
func classifyEmptyResponse(lineCount, bytesRead int, sawDone bool, lastLine string, statusCode int) string {
	// 改进B: 优先提取业务码
	bizCode, bizMsg := extractStatusCode(lastLine)
	var codeTag string
	if bizCode != 0 {
		if bizMsg != "" {
			codeTag = fmt.Sprintf(" [%d:%s]", bizCode, bizMsg)
		} else {
			codeTag = fmt.Sprintf(" [%d]", bizCode)
		}
	}

	if lineCount == 0 {
		return fmt.Sprintf("空body (HTTP %d, 0字节)%s", statusCode, codeTag)
	}
	if sawDone {
		return fmt.Sprintf("[DONE]空流 (HTTP %d, %d行/%d字节, 无content token)%s", statusCode, lineCount, bytesRead, codeTag)
	}
	// 改进A: 末行预览放宽到 200 字节
	preview := lastLine
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}
	return fmt.Sprintf("连接断/异常结束 (HTTP %d, %d行/%d字节, 末行: %q)%s", statusCode, lineCount, bytesRead, preview, codeTag)
}