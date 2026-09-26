package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/relay/constant/role"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/controller/validator"
	"github.com/songquanpeng/one-api/relay/meta"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

func getAndValidateTextRequest(c *gin.Context, relayMode int) (*relaymodel.GeneralOpenAIRequest, error) {
	// dyt-53: Responses API 请求先解析为 Responses 结构，再转换为 chat 请求
	if relayMode == relaymode.Responses {
		var responsesReq OpenAIResponsesRequest
		if err := common.UnmarshalBodyReusable(c, &responsesReq); err != nil {
			return nil, err
		}
		chatReq := responsesToChatRequest(&responsesReq)
		if chatReq.Model == "" {
			return nil, errors.New("model is required")
		}
		if err := validator.ValidateTextRequest(chatReq, relaymode.ChatCompletions); err != nil {
			return nil, err
		}
		return chatReq, nil
	}
	textRequest := &relaymodel.GeneralOpenAIRequest{}
	err := common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}
	if relayMode == relaymode.Moderations && textRequest.Model == "" {
		textRequest.Model = "text-moderation-latest"
	}
	if relayMode == relaymode.Embeddings && textRequest.Model == "" {
		textRequest.Model = c.Param("model")
	}
	err = validator.ValidateTextRequest(textRequest, relayMode)
	if err != nil {
		return nil, err
	}
	return textRequest, nil
}

func getPromptTokens(textRequest *relaymodel.GeneralOpenAIRequest, relayMode int) int {
	switch relayMode {
	case relaymode.ChatCompletions:
		return openai.CountTokenMessages(textRequest.Messages, textRequest.Model)
	case relaymode.Responses:
		// dyt-53: Responses 请求已转换为 chat 格式，按 chat 统计
		return openai.CountTokenMessages(textRequest.Messages, textRequest.Model)
	case relaymode.Completions:
		return openai.CountTokenInput(textRequest.Prompt, textRequest.Model)
	case relaymode.Moderations:
		return openai.CountTokenInput(textRequest.Input, textRequest.Model)
	}
	return 0
}

// preConsumeQuota 自用模式：不做配额预扣，直接放行。
func preConsumeQuota(ctx context.Context, textRequest *relaymodel.GeneralOpenAIRequest, promptTokens int, ratio float64, meta *meta.Meta) (int64, *relaymodel.ErrorWithStatusCode) {
	return 0, nil
}

func getRequestPreview(textRequest *relaymodel.GeneralOpenAIRequest) string {
	if textRequest == nil || len(textRequest.Messages) == 0 {
		return ""
	}
	for _, msg := range textRequest.Messages {
		if msg.Role == "system" {
			continue
		}
		var text string
		switch v := msg.Content.(type) {
		case string:
			text = v
		case []any:
			for _, part := range v {
				if m, ok := part.(map[string]any); ok && m["type"] == "text" {
					if t, ok := m["text"].(string); ok {
						text = t
						break
					}
				}
			}
		}
		if len(text) > 0 {
			// Clean: replace newlines with spaces, trim
			cleaned := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
			runes := []rune(cleaned)
			preview := cleaned
			// 失败日志和无回复日志保留更多请求上下文。
			if len(runes) > 160 {
				preview = string(runes[:160]) + "…"
			}
			return preview
		}
	}
	return ""
}

func postConsumeQuota(ctx context.Context, usage *relaymodel.Usage, meta *meta.Meta, textRequest *relaymodel.GeneralOpenAIRequest, ratio float64, preConsumedQuota int64, modelRatio float64, groupRatio float64, systemPromptReset bool, responseSnippet string) {
	if usage == nil {
		logger.Error(ctx, "usage is nil, which is unexpected")
		return
	}
	promptTokens := usage.PromptTokens
	completionTokens := usage.CompletionTokens
	modelLabel := ""
	if meta.ChannelName != "" {
		modelLabel = fmt.Sprintf("，请求模型：%s/%s", meta.ChannelName, textRequest.Model)
	}
	logContent := responseSnippet
	if logContent != "" {
		logContent = "回复完成" + modelLabel + "，回复内容：" + logContent
	} else {
		logContent = getRequestPreview(textRequest)
		if logContent != "" {
			logContent = "回复完成" + modelLabel + "，回复内容：" + logContent
		} else {
			logContent = fmt.Sprintf("回复完成"+modelLabel+"，回复内容：%d↑ %d↓", promptTokens, completionTokens)
		}
	}
	// dyt-47: 确保上游专用缓存字段映射到标准 CacheReadTokens（probe 不走 openai adaptor）
	if usage.PromptCacheHitTokens > 0 && usage.CacheReadTokens == 0 {
		usage.CacheReadTokens = usage.PromptCacheHitTokens
	}
	if usage.CachedContentTokenCount > 0 && usage.CacheReadTokens == 0 {
		usage.CacheReadTokens = usage.CachedContentTokenCount
	}

	// 自用模式：Quota 字段写入 token 总量（替代计费额度），供 dashboard 统计
	totalTokens := promptTokens + completionTokens
	model.RecordConsumeLog(ctx, &model.Log{
		UserId:                meta.UserId,
		ChannelId:             meta.ChannelId,
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		ModelName:             textRequest.Model,
		TokenName:             meta.TokenName,
		Quota:                 totalTokens,
		Content:               logContent,
		IsStream:              meta.IsStream,
		ElapsedTime:           helper.CalcElapsedTime(meta.StartTime),
		SystemPromptReset:     systemPromptReset,
		CacheReadTokens:       usage.CacheReadTokens,       // dyt-40
		CacheCreationTokens:   usage.CacheCreationTokens,   // dyt-40
		CacheCreation5mTokens: usage.CacheCreation5mTokens, // dyt-40
		CacheCreation1hTokens: usage.CacheCreation1hTokens, // dyt-40
	})
	model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, 0)

	// 记录渠道性能指标到滑动窗口（仅记录一次，完整请求数据）
	monitor.GlobalPerformanceStore.RecordRequest(
		meta.ChannelId,
		promptTokens,
		completionTokens,
		helper.CalcElapsedTime(meta.StartTime),
		0, // TTFT not available here
	)
}

// ---- dyt-111: 消费入账的有界异步派发 ----
//
// postConsumeQuota 原先由 `go postConsumeQuota(...)` 无界启动：每个成功请求一个
// goroutine，且该 goroutine 会一直持有 meta/textRequest（内含整个请求体的 messages
// 与响应片段）直到 DB 写入完成。高并发下 goroutine 与内存无上限增长，
// 属审计指出的 P1 项。
//
// 这里用一个带缓冲的 semaphore 限流：超过上限时**同步执行**而不是丢弃——
// 消费入账影响 dashboard 统计与日志，丢弃会造成静默的数据缺口；
// 同步执行会短暂让请求多等一次 DB 写，属于可控的背压。
var consumeQuotaSem = make(chan struct{}, 256)

func goPostConsumeQuota(ctx context.Context, usage *relaymodel.Usage, meta *meta.Meta,
	textRequest *relaymodel.GeneralOpenAIRequest, ratio float64, preConsumedQuota int64,
	modelRatio float64, groupRatio float64, systemPromptReset bool, responseSnippet string) {
	select {
	case consumeQuotaSem <- struct{}{}:
		go func() {
			defer func() { <-consumeQuotaSem }()
			postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset, responseSnippet)
		}()
	default:
		// 已达并发上限：退化为同步执行，保证不丢数据（有界背压）
		postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, systemPromptReset, responseSnippet)
	}
}

func getMappedModelName(modelName string, mapping map[string]string) (string, bool) {
	if mapping == nil {
		return modelName, false
	}
	mappedModelName := mapping[modelName]
	if mappedModelName != "" {
		return mappedModelName, true
	}
	return modelName, false
}

func isErrorHappened(meta *meta.Meta, resp *http.Response) bool {
	if resp == nil {
		if meta.ChannelType == channeltype.AwsClaude {
			return false
		}
		return true
	}
	if resp.StatusCode != http.StatusOK &&
		// replicate return 201 to create a task
		resp.StatusCode != http.StatusCreated {
		return true
	}
	if meta.ChannelType == channeltype.DeepL {
		// skip stream check for deepl
		return false
	}

	if meta.IsStream && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") &&
		// Even if stream mode is enabled, replicate will first return a task info in JSON format,
		// requiring the client to request the stream endpoint in the task info
		meta.ChannelType != channeltype.Replicate {
		return true
	}
	return false
}

func setSystemPrompt(ctx context.Context, request *relaymodel.GeneralOpenAIRequest, prompt string) (reset bool) {
	if prompt == "" {
		return false
	}
	if len(request.Messages) == 0 {
		return false
	}
	if request.Messages[0].Role == role.System {
		request.Messages[0].Content = prompt
		logger.Infof(ctx, "rewrite system prompt")
		return true
	}
	request.Messages = append([]relaymodel.Message{{
		Role:    role.System,
		Content: prompt,
	}}, request.Messages...)
	logger.Infof(ctx, "add system prompt")
	return true
}
