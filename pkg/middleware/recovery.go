// Package middleware предоставляет gRPC interceptors.
// Файл recovery.go содержит interceptors для обработки паник.
package middleware

import (
	"context"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
)

// RecoveryUnaryInterceptor создает interceptor для обработки паник в unary RPC.
// При возникновении паники логирует stack trace и возвращает codes.Internal клиенту.
func RecoveryUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		// Устанавливаем recover для перехвата паники.
		defer func() {
			if r := recover(); r != nil {
				// Получаем stack trace.
				stack := string(debug.Stack())

				// Извлекаем trace информацию если доступна.
				traceID := TraceIDFromContext(ctx)
				correlationID := CorrelationIDFromContext(ctx)

				// Логируем панику с полным stack trace.
				logger.Error().
					Str("trace_id", traceID).
					Str("correlation_id", correlationID).
					Str("grpc_method", info.FullMethod).
					Interface("panic", r).
					Str("stack", stack).
					Msg("Перехвачена паника в gRPC handler")

				// Возвращаем Internal Error клиенту.
				// Не раскрываем детали паники клиенту по соображениям безопасности.
				err = status.Error(codes.Internal, "Внутренняя ошибка сервера")
			}
		}()

		return handler(ctx, req)
	}
}

// RecoveryStreamInterceptor создает interceptor для обработки паник в stream RPC.
func RecoveryStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		// Устанавливаем recover для перехвата паники.
		defer func() {
			if r := recover(); r != nil {
				// Получаем stack trace.
				stack := string(debug.Stack())

				ctx := ss.Context()

				// Извлекаем trace информацию если доступна.
				traceID := TraceIDFromContext(ctx)
				correlationID := CorrelationIDFromContext(ctx)

				// Логируем панику с полным stack trace.
				logger.Error().
					Str("trace_id", traceID).
					Str("correlation_id", correlationID).
					Str("grpc_method", info.FullMethod).
					Interface("panic", r).
					Str("stack", stack).
					Msg("Перехвачена паника в gRPC stream handler")

				// Возвращаем Internal Error клиенту.
				err = status.Error(codes.Internal, "Внутренняя ошибка сервера")
			}
		}()

		return handler(srv, ss)
	}
}
