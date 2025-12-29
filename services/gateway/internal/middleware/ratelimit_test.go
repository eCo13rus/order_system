package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitMiddleware_AllowsRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Используем miniredis для тестирования
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = redisClient.Close() }()

	mw := NewRateLimitMiddleware(RateLimitConfig{
		Redis:  redisClient,
		Limit:  5,
		Window: time.Minute,
	})

	// Проверяем, что первые 5 запросов проходят
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
		c.Request.RemoteAddr = "192.168.1.1:12345"

		handler := mw.Handle()
		handler(c)

		// Если не заблокирован — код 200 (по умолчанию)
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "запрос %d должен пройти", i+1)
		assert.NotEmpty(t, w.Header().Get("X-RateLimit-Limit"))
		assert.NotEmpty(t, w.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestRateLimitMiddleware_BlocksExcessRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = redisClient.Close() }()

	mw := NewRateLimitMiddleware(RateLimitConfig{
		Redis:  redisClient,
		Limit:  3,
		Window: time.Minute,
	})

	// Отправляем 3 разрешённых запроса
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
		c.Request.RemoteAddr = "10.0.0.1:12345"

		handler := mw.Handle()
		handler(c)

		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "запрос %d должен пройти", i+1)
	}

	// Четвёртый запрос должен быть заблокирован
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.RemoteAddr = "10.0.0.1:12345"

	handler := mw.Handle()
	handler(c)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "rate_limit_exceeded")
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestRateLimitMiddleware_SeparateLimitsPerIP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = redisClient.Close() }()

	mw := NewRateLimitMiddleware(RateLimitConfig{
		Redis:  redisClient,
		Limit:  2,
		Window: time.Minute,
	})

	// IP 1 — исчерпываем лимит
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.RemoteAddr = "1.1.1.1:1234"

		handler := mw.Handle()
		handler(c)
	}

	// IP 1 — следующий запрос заблокирован
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c1.Request.RemoteAddr = "1.1.1.1:1234"
	mw.Handle()(c1)
	assert.Equal(t, http.StatusTooManyRequests, w1.Code)

	// IP 2 — имеет свой лимит
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c2.Request.RemoteAddr = "2.2.2.2:1234"
	mw.Handle()(c2)
	assert.NotEqual(t, http.StatusTooManyRequests, w2.Code, "другой IP имеет свой лимит")
}

func TestRateLimitMiddleware_Headers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = redisClient.Close() }()

	mw := NewRateLimitMiddleware(RateLimitConfig{
		Redis:  redisClient,
		Limit:  10,
		Window: time.Minute,
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "3.3.3.3:1234"

	handler := mw.Handle()
	handler(c)

	// Проверяем заголовки
	assert.Equal(t, "10", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "9", w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
}

func TestRateLimitMiddleware_DefaultValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = redisClient.Close() }()

	// Не указываем Limit и Window — должны использоваться значения по умолчанию
	mw := NewRateLimitMiddleware(RateLimitConfig{
		Redis: redisClient,
	})

	assert.Equal(t, 100, mw.limit)
	assert.Equal(t, time.Minute, mw.window)
}
