package server

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	t.Run("burst 内放行，超限拒绝", func(t *testing.T) {
		rl := newRateLimiter(1, 5)
		base := time.Unix(1700000000, 0)
		rl.now = func() time.Time { return base }

		// burst=5：前 5 次放行
		for i := 0; i < 5; i++ {
			if !rl.allow("mf") {
				t.Fatalf("第 %d 次应放行", i+1)
			}
		}
		// 第 6 次超限
		if rl.allow("mf") {
			t.Fatal("第 6 次应被限流")
		}
	})

	t.Run("令牌随时间恢复", func(t *testing.T) {
		rl := newRateLimiter(1, 5)
		base := time.Unix(1700000000, 0)
		cur := base
		rl.now = func() time.Time { return cur }

		for i := 0; i < 5; i++ {
			rl.allow("mf")
		}
		if rl.allow("mf") {
			t.Fatal("耗尽后应限流")
		}
		// 过 2 秒，rate=1/s → 恢复约 2 个令牌
		cur = base.Add(2 * time.Second)
		if !rl.allow("mf") {
			t.Fatal("2s 后应恢复放行")
		}
	})

	t.Run("不同 project 独立计数", func(t *testing.T) {
		rl := newRateLimiter(1, 5)
		base := time.Unix(1700000000, 0)
		rl.now = func() time.Time { return base }

		for i := 0; i < 5; i++ {
			rl.allow("mf")
		}
		// mf 耗尽，banwen 仍应放行
		if !rl.allow("banwen") {
			t.Fatal("banwen 独立计数应放行")
		}
	})
}
