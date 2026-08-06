package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/controller"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

// https://platform.openai.com/docs/api-reference/chat

func relayHelper(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	var err *model.ErrorWithStatusCode
	switch relayMode {
	case relaymode.ImagesGenerations:
		err = controller.RelayImageHelper(c, relayMode)
	case relaymode.AudioSpeech:
		fallthrough
	case relaymode.AudioTranslation:
		fallthrough
	case relaymode.AudioTranscription:
		err = controller.RelayAudioHelper(c, relayMode)
	case relaymode.Proxy:
		err = controller.RelayProxyHelper(c, relayMode)
	default:
		err = controller.RelayTextHelper(c)
	}
	return err
}

func Relay(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := relaymode.GetByPath(c.Request.URL.Path)
	if config.DebugEnabled {
		requestBody, _ := common.GetRequestBody(c)
		logger.Debugf(ctx, "request body (redacted): %s", redactRequestBody(requestBody))
	}
	channelId := c.GetInt(ctxkey.ChannelId)
	userId := c.GetInt(ctxkey.Id)
	channelName := c.GetString(ctxkey.ChannelName)
	originalModel := c.GetString(ctxkey.OriginalModel)
	bizErr := relayHelper(c, relayMode)
	if bizErr == nil {
		monitor.Emit(channelId, true)
		// 记录成功的请求日志
		// 首次请求成功，不记录尝试日志（消费日志已足够）
		return
	}
	// dyt-39: 用户断开，不重试不记录渠道错误
	if bizErr.StatusCode == 499 {
		c.JSON(499, gin.H{"error": bizErr.Error})
		return
	}
	// 记录首次请求失败的日志
	actualModel := c.GetString(ctxkey.ActualModel)
	dbmodel.RecordChannelAttemptLog(ctx, userId, channelId, channelName, originalModel, actualModel, false, bizErr.Error.Message)
	failedChannelIds := map[int]bool{channelId: true}
	group := c.GetString(ctxkey.Group)
	go processChannelRelayError(ctx, userId, channelId, channelName, *bizErr)
	requestId := c.GetString(helper.RequestIdKey)
	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode) {
		logger.Errorf(ctx, "relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}
	for i := retryTimes; i > 0; i-- {
		var channel *dbmodel.Channel
		var err error
		// Use database query to find a random channel excluding failed ones
		// CacheGetRandomSatisfiedChannel always returns the same channel due to rand seed,
		// so we query DB directly with ORDER BY RANDOM() and exclude failed IDs
		channel, err = dbmodel.GetRandomSatisfiedChannelExcluding(group, originalModel, failedChannelIds)
		if err != nil {
			logger.Errorf(ctx, "GetRandomSatisfiedChannelExcluding failed: %+v", err)
			break
		}
		if channel == nil {
			logger.Warnf(ctx, "no available channel found (all exhausted), giving up")
			break
		}
		// Skip degraded channels (circuit breaker active)
		if monitor.GlobalPerformanceStore.IsDegraded(channel.Id) {
			logger.Debugf(ctx, "skipping degraded channel #%d for retry", channel.Id)
			failedChannelIds[channel.Id] = true
			i++ // Don't consume retry count for degraded channels
			continue
		}
		logger.Infof(ctx, "using channel #%d to retry (remain times %d), failed set: %v", channel.Id, i, failedChannelIds)
		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		bizErr = relayHelper(c, relayMode)
		if bizErr == nil {
			// 重试成功，不记录尝试日志（消费日志已足够）
			return
		}
		// dyt-39: 重试时用户断开，停止重试
		if bizErr.StatusCode == 499 {
			break
		}
		retryChannelId := c.GetInt(ctxkey.ChannelId)
		retryChannelName := c.GetString(ctxkey.ChannelName)
		// 记录重试失败的日志
		retryActualModel := c.GetString(ctxkey.ActualModel)
		dbmodel.RecordChannelAttemptLog(ctx, userId, retryChannelId, retryChannelName, originalModel, retryActualModel, false, bizErr.Error.Message)
		failedChannelIds[retryChannelId] = true
		go processChannelRelayError(ctx, userId, retryChannelId, retryChannelName, *bizErr)
	}
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
		}

		// 拷贝一份 error 再附加 requestId，避免与并发 goroutine 共享的 bizErr 产生竞态
		errCopy := bizErr.Error
		errCopy.Message = helper.MessageWithRequestId(errCopy.Message, requestId)
		c.JSON(bizErr.StatusCode, gin.H{
			"error": errCopy,
		})
	}
}

func shouldRetry(c *gin.Context, statusCode int) bool {
	if _, ok := c.Get(ctxkey.SpecificChannelId); ok {
		return false
	}
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode/100 == 5 {
		return true
	}
	if statusCode == http.StatusBadRequest {
		// 400 表示请求本身无效（参数/格式错误），重试其他渠道不会改变结果，不重试
		return false
	}
	if statusCode/100 == 2 {
		return false
	}
	return true
}

func processChannelRelayError(ctx context.Context, userId int, channelId int, channelName string, err model.ErrorWithStatusCode) {
	logger.Errorf(ctx, "relay error (channel id %d, user id: %d): %s", channelId, userId, err.Message)
	// https://platform.openai.com/docs/guides/error-codes/api-errors
	if monitor.ShouldDisableChannel(&err.Error, err.StatusCode) {
		monitor.DisableChannel(channelId, channelName, err.Message)
	} else {
		monitor.Emit(channelId, false)
	}
	// 记录失败到滑动窗口
	monitor.GlobalPerformanceStore.RecordFailure(channelId, 0)
}

func RelayNotImplemented(c *gin.Context) {
	err := model.Error{
		Message: "API not implemented",
		Type:    "one_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := model.Error{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

// redactRequestBody 脱敏日志中的敏感字段（api_key / key / authorization 等），防止 DEBUG 日志泄露密钥
var relayRedactRegex = regexp.MustCompile(`(?i)("(?:api_key|key|authorization|password|secret|token)"\s*:\s*")([^"]*)(")`)

func redactRequestBody(body []byte) string {
	s := string(body)
	s = relayRedactRegex.ReplaceAllString(s, `${1}***${3}`)
	runes := []rune(s)
	if len(runes) > 2048 {
		s = string(runes[:2048]) + "...(truncated)"
	}
	return s
}
