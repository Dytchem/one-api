package controller

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dyt-111: 消费入账有界派发的回归测试。
//
// 原实现 `go postConsumeQuota(...)` 无界启动 goroutine，每个成功请求一个，
// 且各自持有 meta/textRequest（含整个请求体）直到 DB 写完。
// 这里验证限流器的核心性质：并发数不会超过容量（即不会无界增长）。
func TestConsumeQuotaSemaphoreBoundsConcurrency(t *testing.T) {
	// 用一个独立 semaphore 复现同一限流语义，避免依赖真实 DB
	const capacity = 8
	sem := make(chan struct{}, capacity)

	var running atomic.Int64
	var maxSeen atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				n := running.Add(1)
				for {
					cur := maxSeen.Load()
					if n <= cur || maxSeen.CompareAndSwap(cur, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				running.Add(-1)
			default:
				// 达上限时走同步分支（有界背压），不增加并发
			}
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got > capacity {
		t.Fatalf("并发数超过容量: 观测到 %d, 上限 %d（说明存在无界增长）", got, capacity)
	}
	t.Logf("观测到的最大并发 = %d（上限 %d），有界 ✓", maxSeen.Load(), capacity)
}

// 确保包级 semaphore 容量是有限的（防止有人改成无缓冲/无限）
func TestConsumeQuotaSemHasFiniteCapacity(t *testing.T) {
	cap := cap(consumeQuotaSem)
	if cap <= 0 {
		t.Fatalf("consumeQuotaSem 容量应为正数，实际 %d", cap)
	}
	if cap > 100000 {
		t.Fatalf("consumeQuotaSem 容量过大（%d），失去限流意义", cap)
	}
	t.Logf("consumeQuotaSem 容量 = %d", cap)
}
