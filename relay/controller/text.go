package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/env"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/render"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay"
	"github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/apitype"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

// debugBodyRedactRegex 脱敏 DEBUG 日志中的请求体敏感字段（api_key / key / authorization 等）
var debugBodyRedactRegex = regexp.MustCompile(`(?i)("(?:api_key|key|authorization|password|secret|token)"\s*:\s*")([^"]*)(")`)

func redactDebugBody(body []byte) string {
	s := debugBodyRedactRegex.ReplaceAllString(string(body), `${1}***${3}`)
	runes := []rune(s)
	if len(runes) > 2048 {
		s = string(runes[:2048]) + "...(truncated)"
	}
	return s
}

// isProbeCompatible 探测只对 OpenAI 兼容 SSE 渠道启用。
// 其他渠道（Anthropic/Gemini/AwsClaude/Zhipu/Ali/Baidu/Xunfei 等）的 DoRequest
// 只透传 OpenAI JSON，且 SSE 格式不是 data: {choices...}，探测会永远等超时，
// 直接走上游原始 DoResponse 路径（不探测、不 buffered 回放）。
func isProbeCompatible(meta *meta.Meta) bool {
	return meta.APIType == apitype.OpenAI
}

// newLineScanner 创建支持超长行的 scanner（上限 STREAM_SCANNER_MAX_BUFFER_MB，默认 64MB）
func newLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)
	scanner.Buffer(make([]byte, 64*1024), common.StreamScannerMaxBufferBytes)
	return scanner
}

func compactFailureLogText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

func RelayTextHelper(c *gin.Context) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)
	textRequest, err := getAndValidateTextRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getAndValidateTextRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}
	meta.IsStream = textRequest.Stream

	// dyt-53: Responses 协议仅支持 OpenAI 兼容渠道（请求/响应都按 chat 格式转换）
	if meta.Mode == relaymode.Responses && meta.APIType != apitype.OpenAI {
		return openai.ErrorWrapper(
			fmt.Errorf("Responses API is not supported by channel type %d, use OpenAI compatible channel", meta.ChannelType),
			"unsupported_channel_for_responses", http.StatusBadRequest)
	}

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
	promptTokens := getPromptTokens(textRequest, meta.Mode)
	meta.PromptTokens = promptTokens
	// 自用模式：preConsumeQuota 恒返回 (0, nil)，不再预扣/检查额度
	preConsumedQuota, bizErr := preConsumeQuota(ctx, textRequest, promptTokens, 1, meta)
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
	// 仅对 OpenAI 兼容渠道启用；其他渠道走下方原始透传路径
	if meta.IsStream && isProbeCompatible(meta) {
		// dyt-23: 探测不再克隆请求，直接用 textRequest 的 JSON 作为 body
		// 这样两次 doProbe 发送完全相同的 body 给上游
		textRequest.Stream = true // 探测总是流式
		// dyt-53: 探测请求强制 include_usage，让上游在流里返回 usage
		if textRequest.StreamOptions == nil {
			textRequest.StreamOptions = &model.StreamOptions{}
		}
		textRequest.StreamOptions.IncludeUsage = true

		// 序列化 textRequest 作为 body（保持与原请求字段完全一致）
		probeBodyBytes, _ := json.Marshal(textRequest)

		// 重置 c.Request.Body 让后续 getRequestBody 调用走原 body 路径
		c.Request.Body = io.NopCloser(bytes.NewBuffer(probeBodyBytes))

		// dyt-24: 同渠道重试次数由 CHANNEL_RETRY_COUNT 控制（默认 1）
		retryCount := env.Int("CHANNEL_RETRY_COUNT", 1)
		var attemptSawDone bool                   // 最后一次 attempt 的上游 [DONE] 状态（由 doProbe 写入）
		var respStreamState *responsesStreamState // dyt-53: Responses 流式状态机（跨 attempt 共享最后一次的）
		doProbe := func(attemptNum int) (success bool, probeUsage *model.Usage, responseSnippet string, buf *bytes.Buffer, scanner *bufio.Scanner, statusCode int, errReason string, respBody string) {
			// dyt-53: 每个 attempt 独立的 [DONE] 状态，避免跨 attempt 泄漏
			var sawDone bool
			defer func() { attemptSawDone = sawDone }()
			// dyt-23: 每次重试都从原 bytes 重新创建 reader，确保 body 完全一致
			// dyt-39: 用户断开连接时立即停止
			if ctx.Err() != nil {
				return false, nil, "", nil, nil, 499, "__CANCEL__用户已断开连接", ""
			}

			resp, doErr := adaptor.DoRequest(c, meta, bytes.NewReader(probeBodyBytes))
			if doErr != nil || resp == nil || resp.StatusCode/100 != 2 {
				// dyt-39: 用户断开（客户端取消）→ 499；服务端自身超时不算用户断开
				if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
					return false, nil, "", nil, nil, 499, "__CANCEL__用户已断开连接", ""
				}
				code := 0
				bodyStr := ""
				if resp != nil {
					code = resp.StatusCode
					// dyt-36: 非 2xx 必须读 body（获取上游实际错误 + 关闭连接）
					bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
					if readErr == nil {
						bodyStr = string(bodyBytes)
					}
					resp.Body.Close()
				}
				if doErr != nil {
					return false, nil, "", nil, nil, code, "请求错误: " + doErr.Error(), bodyStr
				}
				// dyt-36: 从 body 提取简洁错误预览，加 [code] 供前端提取
				errPreview := extractUpstreamError(bodyStr, code)
				return false, nil, "", nil, nil, code, fmt.Sprintf("HTTP %d: %s [%d]", code, errPreview, code), bodyStr
			}
			defer resp.Body.Close()

			var localBuf bytes.Buffer
			localBuf.Grow(4096)
			localScanner := newLineScanner(resp.Body)
			var localUsage *model.Usage
			localConfirmed := false
			var localSnippet string

			// dyt-53: Responses 模式流式状态机（chat SSE → responses SSE）
			if meta.Mode == relaymode.Responses {
				respStreamState = newResponsesStreamState(meta.ActualModelName)
			}

			// 改进1：追踪上游返回细节
			lineCount := 0
			bytesRead := 0
			lastLine := ""

			// dyt-20: 异常检测 — finish_reason 出现但缺 usage
			sawFinishReason := false
			finishReasonValue := ""

			// 透传已经开始（数据已发给客户端）后，不能再判定失败触发重试
			startedPassthrough := false

			// dyt-20: 完整响应 body 累积（限 100KB）
			var respBodyBuf bytes.Buffer
			const maxRespBodySize = 100 * 1024
			respBodyTruncated := false

			// dyt-47: SSE首token超时计时起点
			probeStartTime := time.Now()
			// 首个 data: 行到达后，切换为流式整体超时（STREAMING_TIMEOUT，默认 0=跟随 HTTPClient 300s）
			streamingDeadline := time.Time{}

			// dyt-53: 静默超时守卫——上游 0 字节时 Scan 会阻塞，
			// timer 到点后关闭 resp.Body 强制唤醒，避免只能等 HTTPClient 300s
			var confirmedFlag atomic.Bool
			var probeTimedOut atomic.Bool
			probeTimer := time.AfterFunc(time.Duration(config.ProbeTimeout)*time.Second, func() {
				if !confirmedFlag.Load() {
					probeTimedOut.Store(true)
					resp.Body.Close()
				}
			})
			defer probeTimer.Stop()

			// dyt-50: keep-alive 排队超时检测
			keepAliveLineCount := 0
			var keepAliveDeadline time.Time

			for localScanner.Scan() {
				data := localScanner.Text()
				// dyt-39: 用户断开连接时立即停止读取上游
				if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
					return false, nil, "", nil, nil, 499, "__CANCEL__用户已断开连接", respBodyBuf.String()
				}
				lineCount++
				bytesRead += len(data)
				if len(data) > 0 {
					lastLine = data
				}

				// dyt-50: keep-alive 续命（DeepSeek 排队时发 : keep-alive 保活）
				// 完全无 data: → probeStartTime 120s 超时
				// 持续 keep-alive（排队中）→ 等 3×ProbeTimeout（默认 360s）
				if len(data) > 0 && data[:1] == ":" {
					keepAliveLineCount++
					if keepAliveDeadline.IsZero() {
						keepAliveDeadline = time.Now().Add(
							time.Duration(config.ProbeTimeout) * 3 * time.Second)
						// 排队中：静默守卫延长到 3×ProbeTimeout
						probeTimer.Reset(time.Duration(config.ProbeTimeout) * 3 * time.Second)
					}
					if !localConfirmed && !keepAliveDeadline.IsZero() && time.Now().After(keepAliveDeadline) {
						return false, nil, "", nil, nil, resp.StatusCode,
							fmt.Sprintf("排队超时(HTTP %d, keep-alive %d行, %ds无content)", resp.StatusCode, keepAliveLineCount, config.ProbeTimeout*3),
							respBodyBuf.String()
					}
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
					// dyt-88: 探测缓冲设 1MB 上限，防止异常上游灌入无限数据占内存
					if localBuf.Len() < 1<<20 {
						localBuf.WriteString(data)
						localBuf.WriteString("\n")
					}

					// Check for first meaningful content token
					// 兼容 data: xxx 与 data:xxx（无空格）
					if len(data) > 6 && (data[:6] == "data: " || strings.HasPrefix(data, "data:")) {
						// 检测 [DONE] 哨兵（兼容 data: [DONE] 与 data:[DONE]）
						if data == "data: [DONE]" || data == "data:[DONE]" {
							sawDone = true
						} else {
							var streamResp openai.ChatCompletionsStreamResponse
							jsonStart := 6
							if !strings.HasPrefix(data, "data: ") {
								jsonStart = 5
							}
							if jsonErr := json.Unmarshal([]byte(data[jsonStart:]), &streamResp); jsonErr == nil {
								// Content detected via delta.content or reasoning_content
								if len(streamResp.Choices) > 0 {
									delta := &streamResp.Choices[0].Delta
									if content, ok := delta.Content.(string); ok && content != "" {
										localConfirmed = true
									} else if rc, ok := delta.ReasoningContent.(string); ok && rc != "" {
										localConfirmed = true
									} else if len(delta.ToolCalls) > 0 {
										// dyt-26: tool_calls 也是有效响应（M3 流式 tool_calls 时没有 content/reasoning_content）
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

					// dyt-48: SSE首token超时 — 读 PROBE_TIMEOUT 环境变量（默认 120s）
					// keep-alive 排队时使用更长的 keepAliveDeadline
					deadline := probeStartTime.Add(time.Duration(config.ProbeTimeout) * time.Second)
					if !keepAliveDeadline.IsZero() && keepAliveDeadline.After(deadline) {
						deadline = keepAliveDeadline
					}
					if !localConfirmed && time.Now().After(deadline) {
						reason := fmt.Sprintf("SSE首token超时(HTTP %d, %d行/%d字节, %ds内无content)",
							resp.StatusCode, lineCount, bytesRead, config.ProbeTimeout)
						return false, nil, "", nil, nil, resp.StatusCode, reason, respBodyBuf.String()
					}

					if localConfirmed {
						// First content token found: set headers, replay buffer, then passthrough
						logger.Infof(ctx, "stream confirmed with first content token (attempt-%d), replaying %d buffered bytes", attemptNum, localBuf.Len())
						confirmedFlag.Store(true)
						probeTimer.Stop()

						// Write probe confirm log immediately for visibility（异步，不阻塞首 token 关键路径）
						chName := c.GetString(ctxkey.ChannelName)
						chId := c.GetInt(ctxkey.ChannelId)
						probeTTFT := helper.CalcElapsedTime(meta.StartTime)
						probeLogContent := getRequestPreview(textRequest)
						if probeLogContent != "" {
							probeLogContent = fmt.Sprintf("探测成功，渠道：%s(#%d)，模型：%s→%s，请求内容：%s", chName, chId, meta.OriginModelName, meta.ActualModelName, probeLogContent)
						} else {
							probeLogContent = fmt.Sprintf("探测成功，渠道：%s(#%d)，模型：%s→%s，TTFT %dms", chName, chId, meta.OriginModelName, meta.ActualModelName, probeTTFT)
						}
						probeLog := &dbmodel.Log{
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
						}
						go func() {
							logCtx := context.Background()
							dbmodel.RecordConsumeLog(logCtx, probeLog)
						}()

						// 记录 TTFT 到滑动窗口（后续 postConsumeQuota 会记录完整 tok/s）
						go monitor.GlobalPerformanceStore.RecordRequest(
							chId,
							promptTokens,
							0, // 完成 tokens 未知
							probeTTFT,
							probeTTFT, // TTFT = 从开始到首次 content 的耗时
						)

						common.SetEventStreamHeaders(c)

						// dyt-53: Responses 模式回放 buffered chat SSE 时同步转成 responses SSE
						bufReader := bytes.NewReader(localBuf.Bytes())
						lineScanner := newLineScanner(bufReader)
						for lineScanner.Scan() {
							line := lineScanner.Text()
							if len(line) > 0 && !isKeepAliveLine(line) {
								if meta.Mode == relaymode.Responses {
									respStreamState.feedLine(c, line)
								} else {
									render.StringData(c, line)
								}
							}
						}
						localBuf.Reset()
						startedPassthrough = true
						// 透传开始后按流式整体超时限制（STREAMING_TIMEOUT）
						if config.StreamingTimeout > 0 {
							streamingDeadline = time.Now().Add(time.Duration(config.StreamingTimeout) * time.Second)
						}
					}
				} else {
					// Passthrough mode: forward directly
					if len(data) > 0 && !isKeepAliveLine(data) {
						// 转发上游 [DONE]，避免循环结束后重复发送
						if data == "data: [DONE]" || data == "data:[DONE]" {
							sawDone = true
						}
						if meta.Mode == relaymode.Responses {
							respStreamState.feedLine(c, data)
						} else {
							render.StringData(c, data)
						}
					}

					// 流式整体超时（dyt-52）
					if !streamingDeadline.IsZero() && time.Now().After(streamingDeadline) {
						logger.Warnf(ctx, "streaming timeout after %ds, stopping passthrough", config.StreamingTimeout)
						return true, localUsage, localSnippet, &localBuf, localScanner, resp.StatusCode, "", respBodyBuf.String()
					}

					// Accumulate response content snippet from passthrough
					if localSnippet == "" && len(data) > 6 && data[:6] == "data: " && data != "data: [DONE]" {
						var streamResp openai.ChatCompletionsStreamResponse
						if json.Unmarshal([]byte(data[6:]), &streamResp) == nil && len(streamResp.Choices) > 0 {
							delta := &streamResp.Choices[0].Delta
							if c, ok := delta.Content.(string); ok && c != "" {
								runes := []rune(c)
								// dyt-42: 30 → 50，多显示一些详情
								if len(runes) > 50 {
									localSnippet = string(runes[:50]) + "…"
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

			// dyt-53: 静默超时——上游 0 字节时 Scan 因 Body.Close() 退出，
			// 在此判定为首 token 超时
			if !localConfirmed && probeTimedOut.Load() {
				reason := fmt.Sprintf("SSE首token超时(HTTP %d, %d行/%d字节, %ds内无任何数据)",
					resp.StatusCode, lineCount, bytesRead, config.ProbeTimeout)
				return false, nil, "", nil, nil, resp.StatusCode, reason, respBodyBuf.String()
			}

			// dyt-20: 失败判定。考虑 3 种情况：
			// 1) 未收到 content/reasoning token → empty response
			// 2) 收到 finish_reason 但缺 usage 且流末尾不是 [DONE] → 异常断流（仅在未透传前判定）
			// 3) 正常空流（仅 [DONE]）→ 成功
			// 数据已透传给客户端后（startedPassthrough）不再判失败，避免重复内容
			streamAbnormal := !startedPassthrough && sawFinishReason && localUsage == nil && !sawDone
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

		// dyt-24: 探测循环（attempt-0 是原始请求，attempt-1..N 是重试）
		totalAttempts := retryCount + 1
		var lastSuccess bool
		var lastProbeUsage *model.Usage
		var lastResponseSnippet string
		for attempt := 0; attempt < totalAttempts; attempt++ {
			// dyt-39: 用户已断开，跳过后续所有重试
			if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
				dbmodel.RecordCancelLog(ctx, &dbmodel.Log{
					UserId:    meta.UserId,
					TokenName: meta.TokenName,
					ModelName: meta.OriginModelName,
					ChannelId: c.GetInt(ctxkey.ChannelId),
					Content:   "用户断开连接（流式探测中）",
				})
				return openai.ErrorWrapper(ctx.Err(), "request_cancelled", 499)
			}

			success, probeUsage, responseSnippet, _, _, statusCode, errReason, respBody := doProbe(attempt)

			// dyt-39: 检测取消
			if strings.HasPrefix(errReason, "__CANCEL__") {
				dbmodel.RecordCancelLog(ctx, &dbmodel.Log{
					UserId:    meta.UserId,
					TokenName: meta.TokenName,
					ModelName: meta.OriginModelName,
					ChannelId: c.GetInt(ctxkey.ChannelId),
					Content:   "用户断开连接（流式探测上游时）",
				})
				return openai.ErrorWrapper(context.Canceled, "request_cancelled", 499)
			}

			if success {
				lastSuccess = true
				lastProbeUsage = probeUsage
				lastResponseSnippet = responseSnippet
				break
			}

			// 失败：写独立错误日志。列表保留关键上下文，完整请求/响应仍写入 payload。
			attemptLabel := "原始请求"
			if attempt > 0 {
				attemptLabel = fmt.Sprintf("重试-%d", attempt)
			}

			chName := c.GetString(ctxkey.ChannelName)
			chId := c.GetInt(ctxkey.ChannelId)
			requestPreview := compactFailureLogText(getRequestPreview(textRequest), 160)
			responsePreview := compactFailureLogText(respBody, 320)
			requestID := helper.GetRequestID(ctx)
			probeParts := []string{
				fmt.Sprintf("[%s] 探测失败", attemptLabel),
				fmt.Sprintf("渠道：%s(#%d)", chName, chId),
				fmt.Sprintf("模型：%s→%s", meta.OriginModelName, meta.ActualModelName),
				fmt.Sprintf("HTTP：%d", statusCode),
				fmt.Sprintf("请求ID：%s", requestID),
				fmt.Sprintf("上游：%s", errReason),
			}
			if requestPreview != "" {
				probeParts = append(probeParts, "请求内容："+requestPreview)
			}
			if responsePreview != "" {
				probeParts = append(probeParts, "上游响应："+responsePreview)
			}
			probeFailLogContent := strings.Join(probeParts, " | ")

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

			// dyt-24: 异步写 payload — 响应体写 respBody（完整上游响应）
			if failLogId > 0 {
				if reqJSON, ok := c.Get("dyt20_request_json"); ok {
					reqStr, _ := reqJSON.(string)
					dbmodel.RecordLogPayloadAsync(&dbmodel.LogPayload{
						LogId:     failLogId,
						Request:   reqStr,
						Response:  respBody,
						Error:     errReason,
						CreatedAt: meta.StartTime.Unix(),
					})
				}
			}

			logger.Warnf(ctx, "[%s] stream probe returned empty response (attempt %d/%d): %s",
				attemptLabel, attempt+1, totalAttempts, errReason)
		}

		if lastSuccess {
			logger.Infof(ctx, "stream finished with passthrough, usage: %+v", lastProbeUsage)
			if meta.Mode == relaymode.Responses {
				// dyt-53: 补发 responses 完成事件（response.completed 等，幂等）
				respStreamState.finishUp(c)
			} else {
				// 上游已透传 [DONE] 则不再补发（attemptSawDone 为最后成功 attempt 的状态）
				if !attemptSawDone {
					render.Done(c)
				}
			}

			go postConsumeQuota(ctx, lastProbeUsage, meta, textRequest, 1, preConsumedQuota, 0, 0, systemPromptReset, lastResponseSnippet)
			return nil
		}

		return openai.ErrorWrapper(fmt.Errorf("all %d probe attempts returned empty response from channel", totalAttempts), "empty_response", http.StatusBadGateway)
	}

	// Normal flow (non-stream or probe failed)
	requestBody, err := getRequestBody(c, meta, textRequest, adaptor)
	if err != nil {
		return openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
	}

	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		// dyt-39: 用户断开（客户端取消）→ 499；服务端自身超时不算用户断开
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			dbmodel.RecordCancelLog(ctx, &dbmodel.Log{
				UserId:    meta.UserId,
				TokenName: meta.TokenName,
				ModelName: meta.OriginModelName,
				ChannelId: c.GetInt(ctxkey.ChannelId),
				Content:   "用户断开连接（非流式请求上游时）",
			})
			return openai.ErrorWrapper(context.Canceled, "request_cancelled", 499)
		}
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if isErrorHappened(meta, resp) {
		return RelayErrorHandler(resp)
	}

	// 提取非流式回复内容用于日志
	responseSnippet := ""
	if rt, ok := c.Get("response_content"); ok {
		responseSnippet, _ = rt.(string)
	}

	// dyt-53: Responses 非流式——拦截上游 chat JSON，转换为 Responses JSON 写回
	if meta.Mode == relaymode.Responses {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		if readErr != nil {
			resp.Body.Close()
			return openai.ErrorWrapper(readErr, "read_response_body_failed", http.StatusInternalServerError)
		}
		resp.Body.Close()
		// 空响应判定：无内容无 usage 时触发 fallback（与 chat 模式一致）
		var slimResp openai.SlimTextResponse
		isEmpty := false
		if json.Unmarshal(body, &slimResp) == nil && slimResp.Error.Type == "" {
			hasContent := false
			for _, choice := range slimResp.Choices {
				if choice.Message.StringContent() != "" || len(choice.Message.ToolCalls) > 0 {
					hasContent = true
					break
				}
			}
			if !hasContent && slimResp.Usage.TotalTokens == 0 {
				isEmpty = true
			}
		}
		if isEmpty {
			logger.Warnf(ctx, "responses: empty upstream response, triggering fallback")
			chName := c.GetString(ctxkey.ChannelName)
			chId := c.GetInt(ctxkey.ChannelId)
			failLogContent := fmt.Sprintf("回复为空，渠道：%s(#%d)，模型：%s→%s（Responses 模式，无内容无 token）", chName, chId, meta.OriginModelName, meta.ActualModelName)
			failLogId := dbmodel.RecordConsumeLogWithId(ctx, &dbmodel.Log{
				UserId:            meta.UserId,
				TokenName:         meta.TokenName,
				ModelName:         meta.OriginModelName,
				ChannelId:         chId,
				PromptTokens:      promptTokens,
				CompletionTokens:  0,
				Quota:             0,
				Content:           failLogContent,
				IsStream:          false,
				ElapsedTime:       helper.CalcElapsedTime(meta.StartTime),
				SystemPromptReset: systemPromptReset,
			})
			if failLogId > 0 {
				if reqJSON, ok := c.Get("dyt20_request_json"); ok {
					reqStr, _ := reqJSON.(string)
					dbmodel.RecordLogPayloadAsync(&dbmodel.LogPayload{
						LogId:     failLogId,
						Request:   reqStr,
						Response:  string(body),
						Error:     "empty response (responses): no content and no tokens",
						CreatedAt: meta.StartTime.Unix(),
					})
				}
			}
			return openai.ErrorWrapper(fmt.Errorf("empty response from channel"), "empty_response", http.StatusBadGateway)
		}
		for k, v := range resp.Header {
			if strings.EqualFold(k, "Content-Type") {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(http.StatusOK)
		usage := renderResponsesNonStream(c, body, meta.ActualModelName, promptTokens)
		if usage != nil {
			go postConsumeQuota(ctx, usage, meta, textRequest, 1, preConsumedQuota, 0, 0, systemPromptReset, responseSnippet)
		}
		return nil
	}

	usage, respErr := adaptor.DoResponse(c, resp, meta)
	if respErr != nil {
		// dyt-39: 用户断开 → 记终止日志
		if respErr.StatusCode == 499 || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
			dbmodel.RecordCancelLog(ctx, &dbmodel.Log{
				UserId:    meta.UserId,
				TokenName: meta.TokenName,
				ModelName: meta.OriginModelName,
				ChannelId: c.GetInt(ctxkey.ChannelId),
				Content:   "用户断开连接（非流式处理响应时）",
			})
			return openai.ErrorWrapper(context.Canceled, "request_cancelled", 499)
		}
		logger.Errorf(ctx, "respErr is not nil: %+v", respErr)
		return respErr
	}
	// 空响应判定：usage 全 0 且无回复内容才算空。
	// 不再仅凭 CompletionTokens==0 判定，避免误伤 embedding（completion 恒 0）/moderation 等正常响应
	if usage != nil && usage.PromptTokens == 0 && usage.CompletionTokens == 0 && responseSnippet == "" {
		logger.Warnf(ctx, "empty response detected (no tokens, no content), triggering fallback")
		// dyt-20: 记录非流式空响应日志 + payload
		chName := c.GetString(ctxkey.ChannelName)
		chId := c.GetInt(ctxkey.ChannelId)
		failLogContent := fmt.Sprintf("回复为空，渠道：%s(#%d)，模型：%s→%s，无 token 无内容", chName, chId, meta.OriginModelName, meta.ActualModelName)
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
					Error:     "empty response (non-stream): no tokens and no content",
					CreatedAt: meta.StartTime.Unix(),
				})
			}
		}
		return openai.ErrorWrapper(fmt.Errorf("empty response from channel"), "empty_response", http.StatusBadGateway)
	}
	go postConsumeQuota(ctx, usage, meta, textRequest, 1, preConsumedQuota, 0, 0, systemPromptReset, responseSnippet)
	return nil
}

func getRequestBody(c *gin.Context, meta *meta.Meta, textRequest *model.GeneralOpenAIRequest, adaptor adaptor.Adaptor) (io.Reader, error) {
	// dyt-53: Responses 模式必须发送转换后的 chat JSON（原始 body 是 Responses 格式）
	if meta.Mode == relaymode.Responses {
		jsonData, err := json.Marshal(textRequest)
		if err != nil {
			logger.Debugf(c.Request.Context(), "responses request json marshal failed: %s\n", err.Error())
			return nil, err
		}
		return bytes.NewBuffer(jsonData), nil
	}
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
	logger.Debugf(c.Request.Context(), "converted request: \n%s", redactDebugBody(jsonData))
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

// extractUpstreamError dyt-36: 从上游非 2xx body 提取简洁错误预览（120 字内）
func extractUpstreamError(body string, statusCode int) string {
	if body == "" {
		return fmt.Sprintf("HTTP %d (no body)", statusCode)
	}
	// 尝试 OpenAI 格式: {"error":{"message":"..."}}
	var openAIErr struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &openAIErr) == nil && openAIErr.Error.Message != "" {
		s := strings.TrimSpace(openAIErr.Error.Message)
		runes := []rune(s)
		if len(runes) > 120 {
			return string(runes[:120]) + "…"
		}
		return s
	}
	// 尝试通用 message 字段
	var msgResp struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &msgResp) == nil && msgResp.Message != "" {
		s := strings.TrimSpace(msgResp.Message)
		runes := []rune(s)
		if len(runes) > 120 {
			return string(runes[:120]) + "…"
		}
		return s
	}
	// 尝试 "msg" 字段
	var msg2Resp struct {
		Msg string `json:"msg"`
	}
	if json.Unmarshal([]byte(body), &msg2Resp) == nil && msg2Resp.Msg != "" {
		s := strings.TrimSpace(msg2Resp.Msg)
		runes := []rune(s)
		if len(runes) > 120 {
			return string(runes[:120]) + "…"
		}
		return s
	}
	// 回退：取 body 前 120 字
	trimmed := strings.TrimSpace(body)
	runes := []rune(trimmed)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return trimmed
}

// isKeepAliveLine: 过滤上游保活注释行（如 "data: : keep-alive" 或 ": keep-alive"），
// 这类行不是合法 SSE data，会干扰严格客户端（如 pi agent）的流解析
func isKeepAliveLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, ":") ||
		strings.HasPrefix(trimmed, "data: :") ||
		strings.HasPrefix(trimmed, "data::")
}
