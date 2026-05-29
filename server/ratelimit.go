package server

import (
	"sync"
	"time"
)

// rateLimiter 按 key（project/group）的令牌桶限流。
// 进度通道旁路 Alertmanager 后丢失了 AM 的分组兜底，需自带限流防上报方
// 死循环刷爆飞书机器人限频（5 条/秒、100 条/分）。
type rateLimiter struct {
	mu     sync.Mutex
	burst  float64
	rate   float64 // 每秒补充令牌数
	tokens map[string]*bucket
	now    func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerSec, burst float64) *rateLimiter {
	return &rateLimiter{
		burst:  burst,
		rate:   ratePerSec,
		tokens: make(map[string]*bucket),
		now:    time.Now,
	}
}

// allow 返回 key 当前是否放行；放行则消耗一个令牌。
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	b, ok := rl.tokens[key]
	if !ok {
		rl.tokens[key] = &bucket{tokens: rl.burst - 1, last: now}
		return true
	}

	// 按经过时间补充令牌，上限 burst
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
