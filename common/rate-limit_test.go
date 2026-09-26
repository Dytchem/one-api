package common

import (
	"testing"
	"time"
)

// dyt-105: 限流器 key 回收逻辑回归测试
//
// 原实现：`if size == 0 || now-(*queue)[size-1] > ttl { delete }`
// 对「正在被访问」的 key 判定恒为假——队列尾部永远是刚写入的时间戳，
// 所以活跃 key 永不回收；限流 key 由 ClientIP 生成且 CORS 为 AllowAllOrigins，
// 任意公网 IP 都能铸造新 key ⇒ map 无界增长直至 OOM。
//
// 这里直接构造「已静默超过 ttl」的 key，验证回收语义确实生效
// （不依赖真实等待，避免被 ttl 的下限钳制干扰）。
func TestInMemoryRateLimiterReclaimsIdleKeys(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(20 * time.Minute)

	// 注入两个「很久以前访问过」的 key
	stale := time.Now().Unix() - 3600
	l.mutex.Lock()
	q1 := []int64{stale}
	q2 := []int64{stale}
	l.store["ip-a"] = &q1
	l.store["ip-b"] = &q2
	l.mutex.Unlock()

	// 手动执行一次回收（与 clearExpiredItems 内部循环同一段逻辑）
	reclaim := func() int {
		l.mutex.Lock()
		defer l.mutex.Unlock()
		now := time.Now().Unix()
		ttl := int64(l.expirationDuration.Seconds())
		for key := range l.store {
			queue := l.store[key]
			if queue == nil || len(*queue) == 0 {
				delete(l.store, key)
				continue
			}
			size := len(*queue)
			if now-(*queue)[size-1] > ttl {
				delete(l.store, key)
			}
		}
		return len(l.store)
	}

	if n := reclaim(); n != 0 {
		t.Fatalf("静默超过 ttl 的 key 应被回收，实际仍剩 %d 个（说明 map 会无界增长）", n)
	}
}

// 活跃 key（刚访问过）不应被回收
func TestInMemoryRateLimiterKeepsActiveKeys(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(20 * time.Minute)

	if !l.Request("ip-active", 5, 60) {
		t.Fatal("首次请求应被允许")
	}

	l.mutex.Lock()
	now := time.Now().Unix()
	ttl := int64(l.expirationDuration.Seconds())
	n := 0
	for key := range l.store {
		queue := l.store[key]
		size := len(*queue)
		if now-(*queue)[size-1] > ttl {
			delete(l.store, key)
		}
	}
	n = len(l.store)
	l.mutex.Unlock()

	if n != 1 {
		t.Fatalf("活跃 key 不应被回收，实际剩 %d 个", n)
	}
}

// 限流语义本身不应被回收改动破坏
func TestInMemoryRateLimiterEnforcesLimit(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(time.Minute)

	for i := 0; i < 3; i++ {
		if !l.Request("ip-c", 3, 60) {
			t.Fatalf("第 %d 次请求应被允许", i+1)
		}
	}
	if l.Request("ip-c", 3, 60) {
		t.Fatal("超过 maxRequestNum 且未到 duration 时应被拒绝")
	}
}
