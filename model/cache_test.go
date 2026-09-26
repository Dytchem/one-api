package model

import (
	"testing"
	"time"
)

// dyt-108: 无效 token key 负缓存的回归测试。
//
// 背景：CacheGetTokenByKey 原先查库失败直接 return err、**不缓存失败结果**，
// 于是任何随机 `sk-` 键的请求都会打到数据库（找不到也要查一次库）。
// 公网可无上限铸造随机键 ⇒ DB 放大/DoS 面。
// 这里直接验证负缓存原语的读写与失效语义（不依赖真实 DB）。
func TestInvalidTokenNegativeCache(t *testing.T) {
	const key = "sk-definitely-invalid-test-key"

	invalidTokenMemCache.Delete(key)
	// 手动 Set（等价于 CacheGetTokenByKey 命中 gorm.ErrRecordNotFound 时的行为）
	invalidTokenMemCache.Set(key, true)

	if _, ok := invalidTokenMemCache.Get(key); !ok {
		t.Fatal("负缓存应命中，避免每次查库")
	}

	// 令牌变更必须能让负缓存失效，否则新建同名令牌会在 TTL 内一直被判无效
	DeleteTokenMemCache(key)
	if _, ok := invalidTokenMemCache.Get(key); ok {
		t.Fatal("DeleteTokenMemCache 应同时清掉负缓存")
	}
}

// 负缓存必须过期（不能永久把某个 key 钉成无效）
func TestInvalidTokenNegativeCacheExpires(t *testing.T) {
	const key = "sk-expiry-test-key"
	invalidTokenMemCache.Delete(key)

	// 用一个短 TTL 的独立实例验证过期语义，避免等待 30s
	c := newMemCache[bool](50 * time.Millisecond)
	c.Set(key, true)
	if _, ok := c.Get(key); !ok {
		t.Fatal("刚写入应命中")
	}
	time.Sleep(120 * time.Millisecond)
	if _, ok := c.Get(key); ok {
		t.Fatal("超过 TTL 应失效，否则令牌变更后永久不可用")
	}
}

// 负缓存的 TTL 应显著短于正常 token 缓存：既要挡洪峰，又要快速恢复可见性
func TestInvalidTokenCacheTTLIsShort(t *testing.T) {
	if invalidTokenMemCache.ttl <= 0 || invalidTokenMemCache.ttl > 5*time.Minute {
		t.Fatalf("负缓存 TTL 不合理: %v（应在 (0, 5m] 之间）", invalidTokenMemCache.ttl)
	}
}

// ErrTokenNotExist 必须是稳定可比较的错误值
func TestErrTokenNotExistIsStable(t *testing.T) {
	if ErrTokenNotExist == nil {
		t.Fatal("ErrTokenNotExist 不应为 nil")
	}
	if ErrTokenNotExist.Error() == "" {
		t.Fatal("错误信息不应为空")
	}
}
