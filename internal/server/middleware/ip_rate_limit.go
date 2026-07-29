package middleware

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type ipRateLimitEntry struct {
	count   int
	resetAt time.Time
}

// WithIPRateLimit applies a process-local, best-effort fixed-window request limit per client IP.
func WithIPRateLimit(limit int, window time.Duration) gin.HandlerFunc {
	var mu sync.Mutex
	entries := make(map[string]ipRateLimitEntry)
	nextCleanup := time.Now().Add(window)

	return func(c *gin.Context) {
		now := time.Now()
		clientIP := strings.TrimSpace(c.ClientIP())
		if clientIP == "" {
			clientIP = "unknown"
		}

		mu.Lock()
		if !now.Before(nextCleanup) {
			for ip, entry := range entries {
				if !now.Before(entry.resetAt) {
					delete(entries, ip)
				}
			}
			nextCleanup = now.Add(window)
		}

		entry := entries[clientIP]
		if entry.resetAt.IsZero() || !now.Before(entry.resetAt) {
			entry = ipRateLimitEntry{resetAt: now.Add(window)}
		}
		entry.count++
		entries[clientIP] = entry
		allowed := entry.count <= limit
		mu.Unlock()

		if !allowed {
			AbortWithError(c, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}

		c.Next()
	}
}
