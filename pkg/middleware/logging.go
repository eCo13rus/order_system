// Package middleware предоставляет gRPC interceptors.
// Файл logging.go содержит interceptors для логирования запросов.
package middleware

import (
	"context"
	"path"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
)

// LoggingUnaryInterceptor создает interceptor для логирования unary RPC.
// Логирует метод, длительность, статус и trace информацию.
func LoggingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()

		// Извлекаем trace информацию из context.
		traceID := TraceIDFromContext(ctx)
		correlationID := CorrelationIDFromContext(ctx)

		// Извлекаем имя сервиса и метода.
		service := path.Dir(info.FullMethod)[1:]
		method := path.Base(info.FullMethod)

		// Логируем входящий запрос.
		logger.Debug().
			Str("trace_id", traceID).
			Str("correlation_id", correlationID).
			Str("grpc_service", service).
			Str("grpc_method", method).
			Msg("Получен gRPC запрос")

		// Вызываем handler.
		resp, err := handler(ctx, req)

		// Вычисляем длительность.
		duration := time.Since(start)

		// Определяем статус код.
		statusCode := status.Code(err)

		// Формируем базовый лог событие.
		event := logger.Info().
			Str("trace_id", traceID).
			Str("correlation_id", correlationID).
			Str("grpc_service", service).
			Str("grpc_method", method).
			Str("grpc_code", statusCode.String()).
			Dur("duration", duration)

		if err != nil {
			// Логируем ошибку на уровне Error.
			logger.Error().
				Err(err).
				Str("trace_id", traceID).
				Str("correlation_id", correlationID).
				Str("grpc_service", service).
				Str("grpc_method", method).
				Str("grpc_code", statusCode.String()).
				Dur("duration", duration).
				Msg("gRPC запрос завершился с ошибкой")
		} else {
			event.Msg("gRPC запрос выполнен успешно")
		}

		return resp, err
	}
}

// LoggingStreamInterceptor создает interceptor для логирования stream RPC.
func LoggingStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		ctx := ss.Context()

		// Извлекаем trace информацию из context.
		traceID := TraceIDFromContext(ctx)
		correlationID := CorrelationIDFromContext(ctx)

		// Извлекаем имя сервиса и метода.
		service := path.Dir(info.FullMethod)[1:]
		method := path.Base(info.FullMethod)

		// Логируем начало stream.
		logger.Debug().
			Str("trace_id", traceID).
			Str("correlation_id", correlationID).
			Str("grpc_service", service).
			Str("grpc_method", method).
			Bool("client_stream", info.IsClientStream).
			Bool("server_stream", info.IsServerStream).
			Msg("Начат gRPC stream")

		// Вызываем handler.
		err := handler(srv, ss)

		// Вычисляем длительность.
		duration := time.Since(start)

		// Определяем статус код.
		statusCode := status.Code(err)

		if err != nil {
			logger.Error().
				Err(err).
				Str("trace_id", traceID).
				Str("correlation_id", correlationID).
				Str("grpc_service", service).
				Str("grpc_method", method).
				Str("grpc_code", statusCode.String()).
				Dur("duration", duration).
				Msg("gRPC stream завершился с ошибкой")
		} else {
			logger.Info().
				Str("trace_id", traceID).
				Str("correlation_id", correlationID).
				Str("grpc_service", service).
				Str("grpc_method", method).
				Str("grpc_code", statusCode.String()).
				Dur("duration", duration).
				Msg("gRPC stream завершен успешно")
		}

		return err
	}
}
