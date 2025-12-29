// Package middleware содержит HTTP middleware для API Gateway.
package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"example.com/order-system/pkg/logger"
)

// HTTP заголовки для трассировки.
const (
	HeaderTraceID       = "X-Trace-ID"
	HeaderCorrelationID = "X-Correlation-ID"
	HeaderRequestID     = "X-Request-ID" // Алиас для Trace ID
)

// TracingMiddleware — middleware для добавления trace_id и correlation_id.
// Генерирует новые ID если они отсутствуют в запросе.
// ID передаются в context для дальнейшего использования в логах и gRPC вызовах.
type TracingMiddleware struct{}

// NewTracingMiddleware создаёт новый middleware для трассировки.
func NewTracingMiddleware() *TracingMiddleware {
	return &TracingMiddleware{}
}

// Handle возвращает Gin handler function для middleware.
func (m *TracingMiddleware) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Извлекаем или генерируем trace_id
		traceID := c.GetHeader(HeaderTraceID)
		if traceID == "" {
			traceID = c.GetHeader(HeaderRequestID)
		}
		if traceID == "" {
			traceID = uuid.New().String()
		}

		// Извлекаем или генерируем correlation_id
		correlationID := c.GetHeader(HeaderCorrelationID)
		if correlationID == "" {
			correlationID = uuid.New().String()
		}

		// Добавляем ID в контекст запроса (используем pkg/logger для единообразия)
		ctx := logger.NewContextWithIDs(c.Request.Context(), traceID, correlationID)
		c.Request = c.Request.WithContext(ctx)

		// Устанавливаем заголовки в ответ для клиента
		c.Header(HeaderTraceID, traceID)
		c.Header(HeaderCorrelationID, correlationID)

		// Сохраняем в Gin context для удобства
		c.Set("trace_id", traceID)
		c.Set("correlation_id", correlationID)

		// Логируем начало запроса
		log := logger.FromContext(ctx)
		log.Info().
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Str("client_ip", c.ClientIP()).
			Msg("Входящий запрос")

		// Обрабатываем запрос
		c.Next()

		// Логируем завершение запроса
		duration := time.Since(start)
		statusCode := c.Writer.Status()

		logEvent := log.Info()
		if statusCode >= 400 {
			logEvent = log.Warn()
		}
		if statusCode >= 500 {
			logEvent = log.Error()
		}

		logEvent.
			Int("status", statusCode).
			Dur("duration", duration).
			Msg("Запрос завершён")
	}
}
