// Package middleware предоставляет gRPC interceptors.
// Файл tracing.go содержит interceptors для работы с трейсингом.
package middleware

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Ключи для metadata и context.
const (
	// TraceIDKey - ключ для идентификатора трейса в metadata.
	TraceIDKey = "x-trace-id"
	// CorrelationIDKey - ключ для correlation ID в metadata.
	CorrelationIDKey = "x-correlation-id"
)

// contextKey - тип для ключей контекста (избегаем коллизий).
type contextKey string

const (
	// traceIDContextKey - ключ для хранения trace_id в context.
	traceIDContextKey contextKey = "trace_id"
	// correlationIDContextKey - ключ для хранения correlation_id в context.
	correlationIDContextKey contextKey = "correlation_id"
)

// TracingUnaryInterceptor создает interceptor для извлечения/генерации
// trace_id и correlation_id из gRPC metadata.
// Если ID отсутствуют в metadata, генерируются новые UUID.
func TracingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Извлекаем trace информацию из metadata и добавляем в context.
		ctx = extractTraceInfo(ctx)
		return handler(ctx, req)
	}
}

// TracingStreamInterceptor создает interceptor для stream RPC.
// Работает аналогично TracingUnaryInterceptor.
func TracingStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Извлекаем trace информацию и создаем обертку над stream.
		ctx := extractTraceInfo(ss.Context())
		wrappedStream := &tracingServerStream{
			ServerStream: ss,
			ctx:          ctx,
		}
		return handler(srv, wrappedStream)
	}
}

// extractTraceInfo извлекает trace_id и correlation_id из gRPC metadata.
// Если ID не найдены, генерирует новые UUID.
func extractTraceInfo(ctx context.Context) context.Context {
	traceID := ""
	correlationID := ""

	// Пытаемся извлечь из входящей metadata.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(TraceIDKey); len(values) > 0 {
			traceID = values[0]
		}
		if values := md.Get(CorrelationIDKey); len(values) > 0 {
			correlationID = values[0]
		}
	}

	// Генерируем новые ID если не найдены.
	if traceID == "" {
		traceID = uuid.New().String()
	}
	if correlationID == "" {
		correlationID = uuid.New().String()
	}

	// Добавляем в context.
	ctx = context.WithValue(ctx, traceIDContextKey, traceID)
	ctx = context.WithValue(ctx, correlationIDContextKey, correlationID)

	return ctx
}

// TraceIDFromContext извлекает trace_id из context.
// Возвращает пустую строку если не найден.
func TraceIDFromContext(ctx context.Context) string {
	if v := ctx.Value(traceIDContextKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// CorrelationIDFromContext извлекает correlation_id из context.
// Возвращает пустую строку если не найден.
func CorrelationIDFromContext(ctx context.Context) string {
	if v := ctx.Value(correlationIDContextKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// InjectTraceMetadata добавляет trace_id и correlation_id в исходящую metadata.
// Используется при вызове других gRPC сервисов для propagation трейса.
func InjectTraceMetadata(ctx context.Context) context.Context {
	traceID := TraceIDFromContext(ctx)
	correlationID := CorrelationIDFromContext(ctx)

	// Добавляем в исходящую metadata.
	md := metadata.Pairs(
		TraceIDKey, traceID,
		CorrelationIDKey, correlationID,
	)

	// Объединяем с существующей metadata если есть.
	if existingMD, ok := metadata.FromOutgoingContext(ctx); ok {
		md = metadata.Join(existingMD, md)
	}

	return metadata.NewOutgoingContext(ctx, md)
}

// tracingServerStream - обертка над grpc.ServerStream с измененным context.
type tracingServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context возвращает context с добавленной trace информацией.
func (s *tracingServerStream) Context() context.Context {
	return s.ctx
}
