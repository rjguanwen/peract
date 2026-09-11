package middleware

import (
	"sync"
	"time"
)

// RateLimiter 是固定窗口计数器，用于登录失败、找回密码等敏感接口的节流。
//
// 状态保存在进程内存，因此只对单实例部署有效；多实例横向扩容时需换成 Redis 等共享存储。
// 这一点在配置与 README 中都有说明。
type RateLimiter struct {
	mu      sync.Mutex
	windows map[string]*ratelimitWindow
	limit   int
	window  time.Duration
	stop    chan struct{}
	done    sync.Once
}

type ratelimitWindow struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter 创建限流器：每个 key 在 window 内最多允许 limit 次。
// 会拉起一个清理协程回收过期窗口，调用方需在使用完后 Stop。
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	l := &RateLimiter{
		windows: make(map[string]*ratelimitWindow),
		limit:   limit,
		window:  window,
		stop:    make(chan struct{}),
	}
	go l.sweep()
	return l
}

// Allow 消费一次配额，返回是否放行。
func (l *RateLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[key]
	if !ok || now.After(w.resetAt) {
		w = &ratelimitWindow{resetAt: now.Add(l.window)}
		l.windows[key] = w
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}

// RetryAfter 返回当前 key 距窗口重置的剩余时间。
func (l *RateLimiter) RetryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.windows[key]
	if !ok {
		return 0
	}
	if d := time.Until(w.resetAt); d > 0 {
		return d
	}
	return 0
}

// Reset 清空某个 key 的计数，用于登录成功后解除失败锁定。
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, key)
}

// sweep 周期性删除过期窗口，防止 key 无界增长导致的内存缓慢泄漏。
func (l *RateLimiter) sweep() {
	interval := l.window
	if interval > 5*time.Minute {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-ticker.C:
			l.mu.Lock()
			for k, w := range l.windows {
				if now.After(w.resetAt) {
					delete(l.windows, k)
				}
			}
			l.mu.Unlock()
		}
	}
}

// Stop 停止清理协程。
func (l *RateLimiter) Stop() {
	l.done.Do(func() { close(l.stop) })
}
