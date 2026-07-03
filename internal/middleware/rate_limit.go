package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	rate     rate.Limit
	burst    int
}

func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	rl := &RateLimiter{
		limiters: make(map[string]*ipLimiter),
		rate:     r,
		burst:    burst,
	}
	go rl.CleanupLoop()
	return rl
}

func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.limiters[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.limiters[ip] = &ipLimiter{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	entry.lastSeen = time.Now()
	return entry.limiter
}

func (rl *RateLimiter) CleanupLoop() {
	for {
		time.Sleep(5 * time.Minute)
		rl.mu.Lock()
		for ip, entry := range rl.limiters {
			if time.Since(entry.lastSeen) > 10*time.Minute {
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiter := rl.GetLimiter(ip)

		if !limiter.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "TOO_MANY_REQUESTS",
					"message": "rate limit exceeded, please slow down",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
