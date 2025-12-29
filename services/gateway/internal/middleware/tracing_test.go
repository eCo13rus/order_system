package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestTracingMiddleware_GeneratesTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mw := NewTracingMiddleware()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	// Не устанавливаем X-Trace-ID — должен сгенерироваться

	handler := mw.Handle()
	handler(c)

	// Проверяем, что trace_id сгенерирован и установлен в заголовок ответа
	traceID := w.Header().Get(HeaderTraceID)
	assert.NotEmpty(t, traceID, "X-Trace-ID должен быть в ответе")

	// Проверяем, что это валидный UUID
	_, err := uuid.Parse(traceID)
	assert.NoError(t, err, "trace_id должен быть валидным UUID")

	// Проверяем, что trace_id установлен в контекст Gin
	ctxTraceID, exists := c.Get("trace_id")
	assert.True(t, exists, "trace_id должен быть в контексте")
	assert.Equal(t, traceID, ctxTraceID)
}

func TestTracingMiddleware_UsesExistingTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mw := NewTracingMiddleware()
	existingTraceID := "existing-trace-id-12345"

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.Header.Set(HeaderTraceID, existingTraceID)

	handler := mw.Handle()
	handler(c)

	// Проверяем, что используется существующий trace_id
	traceID := w.Header().Get(HeaderTraceID)
	assert.Equal(t, existingTraceID, traceID)

	ctxTraceID, _ := c.Get("trace_id")
	assert.Equal(t, existingTraceID, ctxTraceID)
}

func TestTracingMiddleware_UsesRequestIDAsTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mw := NewTracingMiddleware()
	requestID := "request-id-from-client"

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	// X-Request-ID как альтернатива X-Trace-ID
	c.Request.Header.Set(HeaderRequestID, requestID)

	handler := mw.Handle()
	handler(c)

	// Проверяем, что X-Request-ID используется как trace_id
	traceID := w.Header().Get(HeaderTraceID)
	assert.Equal(t, requestID, traceID)
}

func TestTracingMiddleware_GeneratesCorrelationID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mw := NewTracingMiddleware()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)

	handler := mw.Handle()
	handler(c)

	// Проверяем, что correlation_id сгенерирован
	correlationID := w.Header().Get(HeaderCorrelationID)
	assert.NotEmpty(t, correlationID, "X-Correlation-ID должен быть в ответе")

	// Проверяем, что это валидный UUID
	_, err := uuid.Parse(correlationID)
	assert.NoError(t, err, "correlation_id должен быть валидным UUID")

	// Проверяем контекст
	ctxCorrelationID, exists := c.Get("correlation_id")
	assert.True(t, exists)
	assert.Equal(t, correlationID, ctxCorrelationID)
}

func TestTracingMiddleware_UsesExistingCorrelationID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mw := NewTracingMiddleware()
	existingCorrelationID := "existing-correlation-id"

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.Header.Set(HeaderCorrelationID, existingCorrelationID)

	handler := mw.Handle()
	handler(c)

	// Проверяем, что используется существующий correlation_id
	correlationID := w.Header().Get(HeaderCorrelationID)
	assert.Equal(t, existingCorrelationID, correlationID)
}

func TestTracingMiddleware_SetsAllHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mw := NewTracingMiddleware()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)

	handler := mw.Handle()
	handler(c)

	// Оба заголовка должны быть установлены
	assert.NotEmpty(t, w.Header().Get(HeaderTraceID))
	assert.NotEmpty(t, w.Header().Get(HeaderCorrelationID))
}

func TestNewTracingMiddleware(t *testing.T) {
	mw := NewTracingMiddleware()
	assert.NotNil(t, mw)
}
