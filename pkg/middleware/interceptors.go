// Package middleware предоставляет gRPC interceptors для логирования,
// трейсинга и обработки паник.
package middleware

import (
	"google.golang.org/grpc"
)

// ChainUnaryInterceptors возвращает рекомендуемую цепочку interceptors
// для unary RPC в правильном порядке:
// 1. Recovery - ловит паники (должен быть первым)
// 2. Tracing - извлекает/генерирует trace_id, correlation_id
// 3. Logging - логирует запросы с trace информацией
func ChainUnaryInterceptors() []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		RecoveryUnaryInterceptor(),
		TracingUnaryInterceptor(),
		LoggingUnaryInterceptor(),
	}
}

// ChainStreamInterceptors возвращает рекомендуемую цепочку interceptors
// для stream RPC в правильном порядке.
func ChainStreamInterceptors() []grpc.StreamServerInterceptor {
	return []grpc.StreamServerInterceptor{
		RecoveryStreamInterceptor(),
		TracingStreamInterceptor(),
		LoggingStreamInterceptor(),
	}
}
