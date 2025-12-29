// Package middleware содержит HTTP middleware для API Gateway.
package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"example.com/order-system/pkg/logger"
)

// RateLimitMiddleware — middleware для ограничения количества запросов.
// Использует Redis для хранения счётчиков (sliding window counter).
type RateLimitMiddleware struct {
	redis  *redis.Client
	limit  int           // Максимальное количество запросов
	window time.Duration // Временное окно
}

// RateLimitConfig — конфигурация rate limiter.
type RateLimitConfig struct {
	Redis  *redis.Client
	Limit  int           // Лимит запросов (по умолчанию 100)
	Window time.Duration // Временное окно (по умолчанию 1 минута)
}

// NewRateLimitMiddleware создаёт новый middleware для rate limiting.
func NewRateLimitMiddleware(cfg RateLimitConfig) *RateLimitMiddleware {
	if cfg.Limit <= 0 {
		cfg.Limit = 100
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}

	return &RateLimitMiddleware{
		redis:  cfg.Redis,
		limit:  cfg.Limit,
		window: cfg.Window,
	}
}

// Handle возвращает Gin handler function для middleware.
func (m *RateLimitMiddleware) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		log := logger.FromContext(ctx)

		// Определяем ключ для rate limiting (по IP адресу)
		clientIP := c.ClientIP()
		key := fmt.Sprintf("rate:%s", clientIP)

		// Проверяем и увеличиваем счётчик
		allowed, remaining, err := m.checkLimit(c, key)
		if err != nil {
			// При ошибке Redis пропускаем запрос (fail-open)
			log.Warn().Err(err).Msg("Ошибка проверки rate limit")
			c.Next()
			return
		}

		// Устанавливаем заголовки rate limiting
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", m.limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(m.window).Unix()))

		if !allowed {
			log.Warn().
				Str("client_ip", clientIP).
				Int("limit", m.limit).
				Msg("Rate limit превышен")

			c.Header("Retry-After", fmt.Sprintf("%d", int(m.window.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": fmt.Sprintf("Превышен лимит запросов. Попробуйте через %d секунд", int(m.window.Seconds())),
			})
			return
		}

		c.Next()
	}
}

// checkLimit проверяет и обновляет счётчик запросов.
// Возвращает: (разрешён ли запрос, оставшийся лимит, ошибка).
func (m *RateLimitMiddleware) checkLimit(c *gin.Context, key string) (bool, int, error) {
	ctx := c.Request.Context()

	// Используем INCR + EXPIRE для атомарного увеличения счётчика
	// Lua скрипт для атомарности операции
	script := redis.NewScript(`
		local current = redis.call("INCR", KEYS[1])
		if current == 1 then
			redis.call("EXPIRE", KEYS[1], ARGV[1])
		end
		return current
	`)

	windowSec := int(m.window.Seconds())
	result, err := script.Run(ctx, m.redis, []string{key}, windowSec).Int()
	if err != nil {
		return true, m.limit, err // fail-open при ошибке
	}

	remaining := m.limit - result
	if remaining < 0 {
		remaining = 0
	}

	return result <= m.limit, remaining, nil
}
