package monitor

import (
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

// dyt-104: 原子快照与窗口聚合一致性、驱逐语义、Reset 清零

func TestSnapshotAggregation(t *testing.T) {
	config.ChannelHealthEnabled = true
	s := GlobalPerformanceStore
	const ch = 91001
	s.RemoveChannelMetrics(ch)
	defer s.RemoveChannelMetrics(ch)

	// 3 条完整成功记录 + 1 条 TTFT 探测记录 + 1 条失败
	s.RecordRequest(ch, 10, 20, 1000, 0)
	s.RecordRequest(ch, 10, 30, 2000, 0)
	s.RecordRequest(ch, 10, 40, 3000, 0)
	s.RecordRequest(ch, 10, 0, 300, 300) // probe：只计 probes
	s.RecordFailure(ch, 0)

	m := s.getOrCreate(ch)
	if got := m.snapshotSuccesses.Load(); got != 3 {
		t.Errorf("snapshotSuccesses = %d, want 3", got)
	}
	if got := m.snapshotProbes.Load(); got != 1 {
		t.Errorf("snapshotProbes = %d, want 1", got)
	}
	if got := m.snapshotFailures.Load(); got != 1 {
		t.Errorf("snapshotFailures = %d, want 1", got)
	}
	if got := m.snapshotTokens.Load(); got != 90 {
		t.Errorf("snapshotTokens = %d, want 90", got)
	}
	if got := m.snapshotElapsedMs.Load(); got != 6000 {
		t.Errorf("snapshotElapsedMs = %d, want 6000", got)
	}

	// 成功率 = successes / (successes + failures + probes) = 3/5
	if rate := s.GetRecentSuccessRate(ch); rate != 0.6 {
		t.Errorf("GetRecentSuccessRate = %v, want 0.6", rate)
	}
	// 速度 = 90 tokens / 6s = 15 tok/s
	if speed := s.GetChannelSpeed(ch); speed != 15 {
		t.Errorf("GetChannelSpeed = %v, want 15", speed)
	}
	if score := s.GetHealthScore(ch); score <= 0 || score > 1 {
		t.Errorf("GetHealthScore = %v, want (0, 1]", score)
	}
}

func TestSnapshotEvictsOverwrittenRecords(t *testing.T) {
	config.ChannelHealthEnabled = true
	s := GlobalPerformanceStore
	const ch = 91002
	s.RemoveChannelMetrics(ch)
	defer s.RemoveChannelMetrics(ch)

	// 用失败填满窗口
	for i := 0; i < s.windowSize; i++ {
		s.RecordFailure(ch, 0)
	}
	if rate := s.GetRecentSuccessRate(ch); rate != 0 {
		t.Errorf("success rate after filling failures = %v, want 0", rate)
	}
	// 再用成功覆盖整个窗口：旧失败必须从快照中逐出
	for i := 0; i < s.windowSize; i++ {
		s.RecordRequest(ch, 1, 1, 100, 0)
	}
	m := s.getOrCreate(ch)
	if got := m.snapshotFailures.Load(); got != 0 {
		t.Errorf("snapshotFailures after overwrite = %d, want 0", got)
	}
	if got := m.snapshotSuccesses.Load(); got != int64(s.windowSize) {
		t.Errorf("snapshotSuccesses after overwrite = %d, want %d", got, s.windowSize)
	}
	if rate := s.GetRecentSuccessRate(ch); rate != 1 {
		t.Errorf("success rate after overwrite = %v, want 1", rate)
	}
}

func TestResetChannelMetricsClearsSnapshot(t *testing.T) {
	config.ChannelHealthEnabled = true
	s := GlobalPerformanceStore
	const ch = 91003
	s.RemoveChannelMetrics(ch)
	defer s.RemoveChannelMetrics(ch)

	s.RecordRequest(ch, 10, 20, 1000, 0)
	s.RecordFailure(ch, 0)
	s.ResetChannelMetrics(ch)

	// 重置后无有效记录：各读路径回到默认值
	if rate := s.GetRecentSuccessRate(ch); rate != 1 {
		t.Errorf("success rate after reset = %v, want 1 (default)", rate)
	}
	if score := s.GetHealthScore(ch); score != 1 {
		t.Errorf("health score after reset = %v, want 1 (default)", score)
	}
	if speed := s.GetChannelSpeed(ch); speed != 0 {
		t.Errorf("speed after reset = %v, want 0", speed)
	}
	if ttft := s.GetRecentTTFT(ch); ttft != 0 {
		t.Errorf("ttft after reset = %v, want 0", ttft)
	}
	// 重置后再记录一条成功，统计从干净状态开始
	s.RecordRequest(ch, 1, 2, 200, 0)
	if rate := s.GetRecentSuccessRate(ch); rate != 1 {
		t.Errorf("success rate after reset+1 success = %v, want 1", rate)
	}
}

func TestCircuitBreaker(t *testing.T) {
	config.ChannelHealthEnabled = true
	s := GlobalPerformanceStore
	const ch = 91004
	s.RemoveChannelMetrics(ch)
	defer s.RemoveChannelMetrics(ch)

	threshold := config.CircuitBreakerThreshold
	if threshold <= 0 {
		threshold = 3
	}
	for i := 0; i < threshold-1; i++ {
		s.RecordFailure(ch, 0)
	}
	if s.IsDegraded(ch) {
		t.Errorf("channel degraded before reaching threshold")
	}
	s.RecordFailure(ch, 0)
	if !s.IsDegraded(ch) {
		t.Errorf("channel not degraded after %d consecutive failures", threshold)
	}

	// 手动把 degradedSince 拨到冷却期之外，模拟冷却结束
	m := s.getOrCreate(ch)
	m.mu.Lock()
	cooldown := int64(config.CircuitBreakerCooldown)
	if cooldown <= 0 {
		cooldown = 60
	}
	m.degradedSince = time.Now().Unix() - cooldown - 1
	m.mu.Unlock()
	if s.IsDegraded(ch) {
		t.Errorf("channel still degraded after cooldown elapsed")
	}
}
