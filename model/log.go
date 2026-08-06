package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
)

type Log struct {
	Id                int    `json:"id"`
	UserId            int    `json:"user_id" gorm:"index"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_type"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	ChannelId         int    `json:"channel" gorm:"index"`
	RequestId         string `json:"request_id" gorm:"default:''"`
	ElapsedTime       int64  `json:"elapsed_time" gorm:"default:0"` // unit is ms
	IsStream          bool   `json:"is_stream" gorm:"default:false"`
	SystemPromptReset bool   `json:"system_prompt_reset" gorm:"default:false"`

	// dyt-40: 缓存字段（只记录，不定价）
	CacheReadTokens       int `json:"cache_read_tokens" gorm:"default:0"`
	CacheCreationTokens   int `json:"cache_creation_tokens" gorm:"default:0"`
	CacheCreation5mTokens int `json:"cache_creation_5m_tokens" gorm:"default:0"`
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens" gorm:"default:0"`

	// dyt-93: 失败标记（写入时一次判定，替代查询时全表 LIKE 扫描）
	IsFailed bool `json:"is_failed" gorm:"index:idx_is_failed;default:false"`
}

// isFailContent 与旧 GetFailLogs 的 LIKE 判定保持一致
func isFailContent(content string) bool {
	if strings.Contains(content, "探测失败") || strings.Contains(content, "回复为空") ||
		strings.Contains(content, "状态：失败") || strings.Contains(content, "状态: 失败") ||
		strings.Contains(content, "请求失败") {
		return true
	}
	if strings.Contains(content, "HTTP ") {
		return true
	}
	return false
}

// markLogFailed dyt-93: 失败标记写入时集中判定（消费/测试日志，以及渠道尝试系统日志）
func markLogFailed(log *Log) {
	if log.Type == LogTypeConsume || log.Type == LogTypeTest ||
		(log.Type == LogTypeSystem && strings.HasPrefix(log.Content, "渠道尝试")) {
		log.IsFailed = isFailContent(log.Content)
	}
}

// BackfillFailFlags dyt-93: 一次性历史回填（升级到含 is_failed 列的版本后执行一次）
func BackfillFailFlags() {
	err := LOG_DB.Model(&Log{}).
		Where("type IN (2, 4, 5) AND is_failed = ? AND (content LIKE '%探测失败%' OR content LIKE '%回复为空%' OR content LIKE '%状态：失败%' OR content LIKE '%状态: 失败%' OR content LIKE '%请求失败%' OR content LIKE '%HTTP %')", false).
		Update("is_failed", true).Error
	if err != nil {
		logger.SysError("failed to backfill is_failed flags: " + err.Error())
	}
}

const (
	LogTypeUnknown = iota
	LogTypeTopup
	LogTypeConsume
	LogTypeManage
	LogTypeSystem
	LogTypeTest
	LogTypeCancel
)

func recordLogHelper(ctx context.Context, log *Log) {
	requestId := helper.GetRequestID(ctx)
	log.RequestId = requestId
	// dyt-93: 失败标记写入时集中判定（消费/测试日志，以及渠道尝试系统日志）
	markLogFailed(log)
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
		return
	}
	logger.Infof(ctx, "record log: %+v", log)
}

// recordLogHelperWithId dyt-20: 返回创建的 log id
func recordLogHelperWithId(ctx context.Context, log *Log) int64 {
	requestId := helper.GetRequestID(ctx)
	log.RequestId = requestId
	markLogFailed(log)
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
		return 0
	}
	logger.Infof(ctx, "record log: %+v", log)
	return int64(log.Id)
}

func RecordLog(ctx context.Context, userId int, logType int, content string) {
	if logType == LogTypeConsume && !config.LogConsumeEnabled {
		return
	}
	log := &Log{
		UserId:    userId,
		Username:  GetUsernameById(userId),
		CreatedAt: helper.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	recordLogHelper(ctx, log)
}

func RecordTopupLog(ctx context.Context, userId int, content string, quota int) {
	log := &Log{
		UserId:    userId,
		Username:  GetUsernameById(userId),
		CreatedAt: helper.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Quota:     quota,
	}
	recordLogHelper(ctx, log)
}

func RecordConsumeLog(ctx context.Context, log *Log) {
	if !config.LogConsumeEnabled {
		return
	}
	log.Username = GetUsernameById(log.UserId)
	log.CreatedAt = helper.GetTimestamp()
	log.Type = LogTypeConsume
	recordLogHelper(ctx, log)
}

// RecordConsumeLogWithId dyt-20: 同步写消费日志并返回 log id（供 payload 关联）
func RecordConsumeLogWithId(ctx context.Context, log *Log) int64 {
	if !config.LogConsumeEnabled {
		return 0
	}
	log.Username = GetUsernameById(log.UserId)
	log.CreatedAt = helper.GetTimestamp()
	log.Type = LogTypeConsume
	return recordLogHelperWithId(ctx, log)
}

// RecordChannelAttemptLog dyt-22: 记录渠道尝试日志（去重 empty_response，模型显示 jarvis→MiniMax-M3）
func RecordChannelAttemptLog(ctx context.Context, userId int, channelId int, channelName string, modelName string, actualModel string, success bool, errorMessage string) {
	// dyt-22: 去重 — 探测失败已由 type=2 日志记录，跳过重复的 type=4
	if !success && (strings.Contains(errorMessage, "empty_response") || strings.Contains(errorMessage, "empty response")) {
		return
	}

	// dyt-22: 模型映射显示
	modelDisplay := modelName
	if actualModel != "" && actualModel != modelName {
		modelDisplay = fmt.Sprintf("%s→%s", modelName, actualModel)
	}

	requestID := helper.GetRequestID(ctx)
	log := &Log{
		UserId:    userId,
		Username:  GetUsernameById(userId),
		CreatedAt: helper.GetTimestamp(),
		Type:      LogTypeSystem,
		Content: fmt.Sprintf("渠道尝试 | 渠道：%s(#%d) | 模型：%s | 状态：%s | 请求ID：%s | 错误：%s",
			channelName, channelId, modelDisplay,
			map[bool]string{true: "成功", false: "失败"}[success],
			requestID, errorMessage),
		Quota: 0,
	}
	recordLogHelper(ctx, log)
}

func RecordCancelLog(ctx context.Context, log *Log) {
	log.CreatedAt = helper.GetTimestamp()
	log.Type = LogTypeCancel
	log.Username = GetUsernameById(log.UserId)
	recordLogHelper(ctx, log)
}

func RecordTestLog(ctx context.Context, log *Log) {
	log.CreatedAt = helper.GetTimestamp()
	log.Type = LogTypeTest
	recordLogHelper(ctx, log)
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int) (logs []*Log, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("type = ?", logType)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	return logs, err
}

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int) (logs []*Log, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("user_id = ? and type = ?", userId, logType)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Omit("id").Find(&logs).Error
	return logs, err
}

func SearchAllLogs(keyword string) (logs []*Log, err error) {
	err = LOG_DB.Where("type = ? or content LIKE ?", keyword, keyword+"%").Order("id desc").Limit(config.MaxRecentItems).Find(&logs).Error
	return logs, err
}

func SearchUserLogs(userId int, keyword string) (logs []*Log, err error) {
	err = LOG_DB.Where("user_id = ? and type = ?", userId, keyword).Order("id desc").Limit(config.MaxRecentItems).Omit("id").Find(&logs).Error
	return logs, err
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int) (quota int64) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").Select(fmt.Sprintf("%s(sum(quota),0)", ifnull))
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&quota)
	return quota
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").Select(fmt.Sprintf("%s(sum(prompt_tokens),0) + %s(sum(completion_tokens),0)", ifnull, ifnull))
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func DeleteOldLog(targetTimestamp int64) (int64, error) {
	result := LOG_DB.Where("created_at < ?", targetTimestamp).Delete(&Log{})
	return result.RowsAffected, result.Error
}

type LogStatistic struct {
	Day              string `gorm:"column:day"`
	ModelName        string `gorm:"column:model_name"`
	RequestCount     int    `gorm:"column:request_count"`
	Quota            int    `gorm:"column:quota"`
	PromptTokens     int    `gorm:"column:prompt_tokens"`
	CompletionTokens int    `gorm:"column:completion_tokens"`
}

func SearchLogsByDayAndModel(userId, start, end int) (LogStatistics []*LogStatistic, err error) {
	groupSelect := "DATE_FORMAT(FROM_UNIXTIME(created_at), '%Y-%m-%d') as day"

	if common.UsingPostgreSQL {
		groupSelect = "TO_CHAR(date_trunc('day', to_timestamp(created_at)), 'YYYY-MM-DD') as day"
	}

	if common.UsingSQLite {
		groupSelect = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch')) as day"
	}

	err = LOG_DB.Raw(`
		SELECT `+groupSelect+`,
		model_name, count(1) as request_count,
		sum(quota) as quota,
		sum(prompt_tokens) as prompt_tokens,
		sum(completion_tokens) as completion_tokens
		FROM logs
		WHERE type=2
		AND user_id= ?
		AND created_at BETWEEN ? AND ?
		GROUP BY day, model_name
		ORDER BY day, model_name
	`, userId, start, end).Scan(&LogStatistics).Error

	return LogStatistics, err
}

// LogPayload 完整保留失败请求/响应的 payload（dyt-20 新增）
type LogPayload struct {
	LogId     int64  `gorm:"primaryKey"` // 一对一关联 logs.id
	Request   string `gorm:"type:longtext"`
	Response  string `gorm:"type:longtext"`
	Error     string `gorm:"type:longtext"`
	CreatedAt int64
}

// payloadQueue 异步写队列，避免阻塞请求
var payloadQueue = make(chan *LogPayload, 2048)

// dyt-33: payload 默认保留 7 天（主人可设 LOG_PAYLOAD_TTL_HOURS 环境变量覆盖）
// 负值 = 禁用清理；0 = 使用默认 7 天。每 24h 清理一次。
// 长文本 payload 可能 100KB+ / 条，不定期清理会无限增长
const defaultPayloadTTLHours = 7 * 24

func getPayloadTTLHours() int {
	ttl := config.LogPayloadTTLHours
	if ttl < 0 {
		// 负数 = 禁用清理（不推荐）
		return -1
	}
	if ttl == 0 {
		return defaultPayloadTTLHours
	}
	return ttl
}

func CleanupOldPayloads() {
	ttl := getPayloadTTLHours()
	if ttl < 0 {
		return // 禁用清理
	}
	cutoff := helper.GetTimestamp() - int64(ttl*3600)
	result := payloadDB().Where("created_at < ?", cutoff).Delete(&LogPayload{})
	if result.Error != nil {
		logger.SysError("failed to cleanup old payloads: " + result.Error.Error())
		return
	}
	if result.RowsAffected > 0 {
		logger.SysLog(fmt.Sprintf("cleaned up %d old log_payloads entries (TTL=%dh)", result.RowsAffected, ttl))
	}
}

func init() {
	// 启动 2 个 worker 消费队列
	for i := 0; i < 2; i++ {
		go func() {
			for p := range payloadQueue {
				if err := payloadDB().Create(p).Error; err != nil {
					logger.SysError("failed to record log payload: " + err.Error())
				}
			}
		}()
	}
	// dyt-33: 启动 payload 定期清理任务，每 24h 跑一次
	go func() {
		// 启动后等 1 小时再首次跑，避免启动期 IO 竞争
		time.Sleep(1 * time.Hour)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			CleanupOldPayloads()
		}
	}()
}

func payloadDB() *gorm.DB {
	return LOG_DB
}

// RecordLogPayloadAsync 异步写 payload（不阻塞）
func RecordLogPayloadAsync(payload *LogPayload) {
	select {
	case payloadQueue <- payload:
	default:
		// 队列满则丢弃，避免内存泄漏
		logger.SysLog(fmt.Sprintf("payload queue full, dropped log_id=%d", payload.LogId))
	}
}

// RecordLogPayload 同步写 payload（用于测试/补录）
func RecordLogPayload(payload *LogPayload) error {
	return payloadDB().Create(payload).Error
}

// GetLogPayload 按 logId 查 payload
func GetLogPayload(logId int64) (*LogPayload, error) {
	var p LogPayload
	err := payloadDB().Where("log_id = ?", logId).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// LogHasPayload dyt-20: 检查某 log 是否有 payload
func LogHasPayload(logId int64) bool {
	var cnt int64
	payloadDB().Model(&LogPayload{}).Where("log_id = ?", logId).Count(&cnt)
	return cnt > 0
}

// BatchHasPayload dyt-52: 批量检查 logs 是否有 payload（替代 N+1 查询）
func BatchHasPayload(logIds []int64) map[int64]bool {
	result := make(map[int64]bool, len(logIds))
	if len(logIds) == 0 {
		return result
	}
	var ids []int64
	payloadDB().Model(&LogPayload{}).Where("log_id IN ?", logIds).Pluck("log_id", &ids)
	for _, id := range ids {
		result[id] = true
	}
	return result
}

// GetFailLogs 失败日志分页列表：探测、渠道尝试和测试失败都纳入。
// dyt-93: 改用写入时判定的 is_failed 索引列（历史数据该列为 false，不再回填），
// ORDER BY id DESC 保证同秒日志分页稳定（created_at 为秒级整数）。
func GetFailLogs(channelId int, modelName string, startTimestamp, endTimestamp int64, offset, size int) ([]*Log, int64, error) {
	var logs []*Log
	var total int64

	query := LOG_DB.Model(&Log{}).
		Where("type IN (2, 4, 5)").
		Where("is_failed = ?", true)

	if channelId > 0 {
		query = query.Where("channel_id = ?", channelId)
	}
	if modelName != "" {
		query = query.Where("model_name LIKE ?", "%"+modelName+"%")
	}
	if startTimestamp > 0 {
		query = query.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp > 0 {
		query = query.Where("created_at <= ?", endTimestamp)
	}

	// count
	query.Count(&total)

	// order by id desc（单调稳定），paginate
	err := query.
		Order("id DESC").
		Offset(offset).Limit(size).
		Find(&logs).Error

	return logs, total, err
}
