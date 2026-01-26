package logger

import (
	"context"

	"github.com/rs/zerolog"
)

// Ключи для хранения значений в контексте.
// Используем приватные типы для избежания коллизий с другими пакетами.
type ctxKey string

const (
	// traceIDKey - ключ для хранения trace_id в контексте.
	// Trace ID используется для отслеживания запроса через все сервисы.
	traceIDKey ctxKey = "trace_id"

	// correlationIDKey - ключ для хранения correlation_id в контексте.
	// Correlation ID связывает связанные запросы (например, все операции в рамках одного заказа).
	correlationIDKey ctxKey = "correlation_id"

	// loggerKey - ключ для хранения логгера в контексте.
	// Позволяет передавать настроенный логгер через context.
	loggerKey ctxKey = "logger"
)

// WithTraceID добавляет trace_id в контекст.
// Trace ID должен быть уникальным идентификатором запроса,
// обычно генерируется на входе в систему (API Gateway).
//
// Пример:
//
//	ctx = logger.WithTraceID(ctx, "abc-123-def")
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceIDFromContext извлекает trace_id из контекста.
// Возвращает пустую строку, если trace_id не установлен.
func TraceIDFromContext(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDKey).(string); ok {
		return traceID
	}
	return ""
}

// WithCorrelationID добавляет correlation_id в контекст.
// Correlation ID используется для связывания нескольких запросов,
// относящихся к одной бизнес-операции.
//
// Пример:
//
//	ctx = logger.WithCorrelationID(ctx, "order-789")
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationIDKey, correlationID)
}

// CorrelationIDFromContext извлекает correlation_id из контекста.
// Возвращает пустую строку, если correlation_id не установлен.
func CorrelationIDFromContext(ctx context.Context) string {
	if correlationID, ok := ctx.Value(correlationIDKey).(string); ok {
		return correlationID
	}
	return ""
}

// WithLogger добавляет логгер в контекст.
// Полезно для передачи настроенного логгера через слои приложения.
//
// Пример:
//
//	log := logger.With().Str("service", "payment").Logger()
//	ctx = logger.WithLogger(ctx, log)
func WithLogger(ctx context.Context, l zerolog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext извлекает логгер из контекста и автоматически добавляет
// trace_id и correlation_id, если они присутствуют в контексте.
//
// Если логгер не был явно добавлен в контекст, возвращает глобальный логгер.
// Это основной способ получения логгера в обработчиках и сервисах.
//
// Пример:
//
//	func (s *Service) ProcessOrder(ctx context.Context, orderID string) error {
//	    log := logger.FromContext(ctx)
//	    log.Info().Str("order_id", orderID).Msg("Начало обработки заказа")
//	    // ...
//	}
func FromContext(ctx context.Context) zerolog.Logger {
	// Пытаемся получить логгер из контекста.
	var l zerolog.Logger
	if ctxLogger, ok := ctx.Value(loggerKey).(zerolog.Logger); ok {
		l = ctxLogger
	} else {
		// Используем глобальный логгер, если в контексте его нет.
		l = log
	}

	// Добавляем trace_id, если он есть в контексте.
	if traceID := TraceIDFromContext(ctx); traceID != "" {
		l = l.With().Str("trace_id", traceID).Logger()
	}

	// Добавляем correlation_id, если он есть в контексте.
	if correlationID := CorrelationIDFromContext(ctx); correlationID != "" {
		l = l.With().Str("correlation_id", correlationID).Logger()
	}

	return l
}

// Ctx возвращает указатель на zerolog.Logger из контекста.
// Это альтернативный способ использования, совместимый с zerolog.Ctx().
//
// Пример:
//
//	log := logger.Ctx(ctx)
//	log.Info().Msg("Сообщение")
func Ctx(ctx context.Context) *zerolog.Logger {
	l := FromContext(ctx)
	return &l
}

func NewContextWithIDs(ctx context.Context, traceID, correlationID string) context.Context {
	if traceID != "" {
		ctx = WithTraceID(ctx, traceID)
	}
	if correlationID != "" {
		ctx = WithCorrelationID(ctx, correlationID)
	}
	return ctx
}
