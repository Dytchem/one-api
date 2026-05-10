package monitor

import (
	"sort"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
)

// RequestRecord 单次请求的性能记录
type RequestRecord struct {
	Success         bool
	PromptTokens    int
	CompletionTokens int
	ElapsedMs       int64  // 总耗时（ms）
	TTFTMs          int64  // 首个 token 等待时间（ms），0 表示非流式
}

// ChannelMetrics 单个渠道的滑动窗口数据
type ChannelMetrics struct {
	mu      sync.RWMutex
	records []RequestRecord // 环形缓冲区，按时间顺序
	head    int             // 下一条写入位置
	count   int             // 当前记录数
	size    int             // 窗口大小

	// 熔断器状态
	consecutiveFailures int       // 连续失败计数
	degradedSince       int64      // 进入 degraded 状态的时间戳（unix sec），0 表示正常
}

// PerformanceStore 全局渠道性能存储
type PerformanceStore struct {
	mu       sync.RWMutex
	channels map[int]*ChannelMetrics // channelId -> metrics
	windowSize int
	failWeight float64
}

// 全局单例
var GlobalPerformanceStore *PerformanceStore

func init() {
	windowSize := 20
	if config.ChannelHealthWindowSize > 0 {
		windowSize = config.ChannelHealthWindowSize
	}
	failWeight := 3.0
	if config.ChannelHealthFailWeight > 0 {
		failWeight = config.ChannelHealthFailWeight
	}
	GlobalPerformanceStore = &PerformanceStore{
		channels:   make(map[int]*ChannelMetrics),
		windowSize: windowSize,
		failWeight: failWeight,
	}
}

// getOrCreate 获取或创建某渠道的 metrics
func (s *PerformanceStore) getOrCreate(channelId int) *ChannelMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.channels[channelId]; ok {
		return m
	}

	m := &ChannelMetrics{
		records: make([]RequestRecord, s.windowSize),
		size:    s.windowSize,
	}
	s.channels[channelId] = m
	return m
}

// RecordRequest 记录一次成功的请求（由 postConsumeQuota 调用）
func (s *PerformanceStore) RecordRequest(channelId int, promptTokens, completionTokens int, elapsedMs, ttftMs int64) {
	if !config.ChannelHealthEnabled {
		return
	}

	m := s.getOrCreate(channelId)

	m.mu.Lock()
	defer m.mu.Unlock()

	// 写入环形缓冲区
	m.records[m.head] = RequestRecord{
		Success:         true,
		PromptTokens:    promptTokens,
		CompletionTokens: completionTokens,
		ElapsedMs:       elapsedMs,
		TTFTMs:          ttftMs,
	}
	m.head = (m.head + 1) % m.size
	if m.count < m.size {
		m.count++
	}

	// 成功后重置连续失败计数
	m.consecutiveFailures = 0
}

// RecordFailure 记录一次失败的请求（由 processChannelRelayError 调用）
func (s *PerformanceStore) RecordFailure(channelId int, elapsedMs int64) {
	if !config.ChannelHealthEnabled {
		return
	}

	m := s.getOrCreate(channelId)

	m.mu.Lock()
	defer m.mu.Unlock()

	// 写入环形缓冲区（失败记录只有 ElapsedMs 有效）
	m.records[m.head] = RequestRecord{
		Success:   false,
		ElapsedMs: elapsedMs,
	}
	m.head = (m.head + 1) % m.size
	if m.count < m.size {
		m.count++
	}

	// 增加连续失败计数
	m.consecutiveFailures++

	// 如果超过阈值，标记为 degraded
	threshold := config.CircuitBreakerThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if m.consecutiveFailures >= threshold && m.degradedSince == 0 {
		m.degradedSince = time.Now().Unix()
	}
}

// IsDegraded 检查渠道是否处于熔断状态
func (s *PerformanceStore) IsDegraded(channelId int) bool {
	if !config.ChannelHealthEnabled {
		return false
	}

	s.mu.RLock()
	m, ok := s.channels[channelId]
	s.mu.RUnlock()

	if !ok || m == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.degradedSince == 0 {
		return false
	}

	cooldown := int64(config.CircuitBreakerCooldown)
	if cooldown <= 0 {
		cooldown = 60
	}
	if time.Now().Unix()-m.degradedSince >= cooldown {
		// 冷却时间到，重置为正常
		m.degradedSince = 0
		m.consecutiveFailures = 0
		return false
	}

	return true
}

// GetHealthScore 计算渠道健康度得分（0.0 ~ 1.0）
// 得分 = 成功率权重(0.7) * successRate + 速度权重(0.3) * normalizedSpeed
func (s *PerformanceStore) GetHealthScore(channelId int) float64 {
	if !config.ChannelHealthEnabled {
		return 1.0 // 默认全部正常
	}

	s.mu.RLock()
	m, ok := s.channels[channelId]
	s.mu.RUnlock()

	if !ok || m == nil || m.count == 0 {
		return 1.0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// 计算加权成功次数和失败次数
	var weightedSuccesses float64
	var weightedFailures float64
	var totalCompletionTokens int
	var totalElapsedMs int64

	for i := 0; i < m.count; i++ {
		record := m.records[i]
		if record.Success {
			weightedSuccesses += 1.0
			totalCompletionTokens += record.CompletionTokens
			totalElapsedMs += record.ElapsedMs
		} else {
			weightedFailures += s.failWeight
		}
	}

	total := weightedSuccesses + weightedFailures
	if total == 0 {
		return 1.0
	}

	successRate := weightedSuccesses / total

	// 归一化速度：tok/s = completionTokens / (elapsedMs / 1000)
	// 速度越快得分越高，设 50 tok/s 为满分
	var speedScore float64 = 0.5 // 默认中等速度
	if totalElapsedMs > 0 {
		toksPerSec := float64(totalCompletionTokens) / (float64(totalElapsedMs) / 1000.0)
		// 50 tok/s 以上给满分，线性压缩
		speedScore = toksPerSec / 50.0
		if speedScore > 1.0 {
			speedScore = 1.0
		}
		if speedScore < 0.1 {
			speedScore = 0.1 // 最低 0.1
		}
	}

	// 综合得分
	healthScore := successRate*0.7 + speedScore*0.3
	return healthScore
}

// GetChannelSpeed 获取渠道平均速度（tok/s）
func (s *PerformanceStore) GetChannelSpeed(channelId int) float64 {
	if !config.ChannelHealthEnabled {
		return 0
	}

	s.mu.RLock()
	m, ok := s.channels[channelId]
	s.mu.RUnlock()

	if !ok || m == nil || m.count == 0 {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalCompletionTokens int
	var totalElapsedMs int64
	hasData := false

	for i := 0; i < m.count; i++ {
		record := m.records[i]
		if record.Success && record.ElapsedMs > 0 {
			totalCompletionTokens += record.CompletionTokens
			totalElapsedMs += record.ElapsedMs
			hasData = true
		}
	}

	if !hasData || totalElapsedMs == 0 {
		return 0
	}

	return float64(totalCompletionTokens) / (float64(totalElapsedMs) / 1000.0)
}

// GetRecentTTFT 获取渠道近期平均 TTFT（ms）
func (s *PerformanceStore) GetRecentTTFT(channelId int) int64 {
	if !config.ChannelHealthEnabled {
		return 0
	}

	s.mu.RLock()
	m, ok := s.channels[channelId]
	s.mu.RUnlock()

	if !ok || m == nil || m.count == 0 {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalTTFT int64
	var count int

	for i := 0; i < m.count; i++ {
		record := m.records[i]
		if record.Success && record.TTFTMs > 0 {
			totalTTFT += record.TTFTMs
			count++
		}
	}

	if count == 0 {
		return 0
	}

	return totalTTFT / int64(count)
}

// GetRecentSuccessRate 获取近期成功率（0.0 ~ 1.0）
func (s *PerformanceStore) GetRecentSuccessRate(channelId int) float64 {
	if !config.ChannelHealthEnabled {
		return 1.0
	}

	s.mu.RLock()
	m, ok := s.channels[channelId]
	s.mu.RUnlock()

	if !ok || m == nil || m.count == 0 {
		return 1.0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	successes := 0
	for i := 0; i < m.count; i++ {
		if m.records[i].Success {
			successes++
		}
	}

	return float64(successes) / float64(m.count)
}

// FilterAbilities 过滤掉 degraded 的渠道并按健康度排序，返回排序后的能力列表
// failedIds 为 nil 时不额外排除
func FilterAbilities(abilities []model.Ability, failedIds map[int]bool) []model.Ability {
	type abilityWithScore struct {
		ability model.Ability
		score   float64
	}
	var scored []abilityWithScore
	for _, a := range abilities {
		if failedIds != nil && failedIds[a.ChannelId] {
			continue
		}
		if GlobalPerformanceStore.IsDegraded(a.ChannelId) {
			continue
		}
		score := GlobalPerformanceStore.GetHealthScore(a.ChannelId)
		scored = append(scored, abilityWithScore{ability: a, score: score})
	}
	// 按健康度降序排列
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	result := make([]model.Ability, len(scored))
	for i, s := range scored {
		result[i] = s.ability
	}
	return result
}

// ResetChannelMetrics 重置某渠道的指标（用于测试或管理）
func (s *PerformanceStore) ResetChannelMetrics(channelId int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.channels[channelId]; ok && m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.head = 0
		m.count = 0
		m.consecutiveFailures = 0
		m.degradedSince = 0
	}
}

// GetRecordCount 获取滑动窗口当前记录数
func (s *PerformanceStore) GetRecordCount(channelId int) int {
	s.mu.RLock()
	m, ok := s.channels[channelId]
	s.mu.RUnlock()
	if !ok || m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}
