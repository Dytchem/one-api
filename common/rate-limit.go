package common

import (
	"sync"
	"time"
)

type InMemoryRateLimiter struct {
	store              map[string]*[]int64
	mutex              sync.Mutex
	expirationDuration time.Duration
}

func (l *InMemoryRateLimiter) Init(expirationDuration time.Duration) {
	if l.store == nil {
		l.mutex.Lock()
		if l.store == nil {
			l.store = make(map[string]*[]int64)
			l.expirationDuration = expirationDuration
			if expirationDuration > 0 {
				go l.clearExpiredItems()
			}
		}
		l.mutex.Unlock()
	}
}

func (l *InMemoryRateLimiter) clearExpiredItems() {
	for {
		l.mutex.Lock()
		d := l.expirationDuration
		l.mutex.Unlock()
		if d <= 0 {
			return
		}
		time.Sleep(d)
		l.mutex.Lock()
		now := time.Now().Unix()
		ttl := int64(d.Seconds())
		if ttl < 1 {
			ttl = 1
		}
		// dyt-105: 回收语义修正。原实现读 l.expirationDuration 时未持锁（data race），
		// 且判定对"正在被访问"的 key 永远为假——队列尾部是刚写入的时间戳，
		// 于是活跃 key 永不回收。限流 key 由 `mark + ClientIP()` 生成、
		// CORS 为 AllowAllOrigins，任意公网 IP 都能铸造新 key ⇒ map 无界增长直至 OOM。
		// 正确语义：以队列中最后一次请求时间为准，静默超过 ttl 即回收。
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
		l.mutex.Unlock()
	}
}

// Request parameter duration's unit is seconds
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	// [old <-- new]
	queue, ok := l.store[key]
	now := time.Now().Unix()
	if ok {
		if len(*queue) < maxRequestNum {
			*queue = append(*queue, now)
			return true
		} else {
			if now-(*queue)[0] >= duration {
				*queue = (*queue)[1:]
				*queue = append(*queue, now)
				return true
			} else {
				return false
			}
		}
	} else {
		s := make([]int64, 0, maxRequestNum)
		l.store[key] = &s
		*(l.store[key]) = append(*(l.store[key]), now)
	}
	return true
}
