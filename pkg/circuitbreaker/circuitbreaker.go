// Package circuitbreaker предоставляет Circuit Breaker для защиты от каскадных сбоев.
// Используется в gRPC клиентах для быстрого отказа при недоступности сервиса.
//
// Состояния Circuit Breaker:
//   - Closed: нормальная работа, запросы проходят
//   - Open: сервис недоступен, запросы отклоняются мгновенно (без ожидания timeout)
//   - Half-Open: пробный период, пропускаем часть запросов для проверки восстановления
//
// Использование:
//
//	cb := circuitbreaker.New("user-service")
//	conn, _ := grpc.NewClient(addr,
//	    grpc.WithUnaryInterceptor(circuitbreaker.UnaryClientInterceptor(cb)),
//	)
package circuitbreaker

import (
	"context"
	"time"

	"github.com/sony/gobreaker/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
)

// Settings — настройки Circuit Breaker.
type Settings struct {
	MaxRequests  uint32        // Макс. запросов в Half-Open состоянии (по умолчанию 1)
	Interval     time.Duration // Интервал сброса счётчика в Closed (по умолчанию 60s)
	Timeout      time.Duration // Время в Open до перехода в Half-Open (по умолчанию 30s)
	FailureRatio float64       // Доля ошибок для перехода в Open (по умолчанию 0.5)
	MinRequests  uint32        // Мин. запросов для расчёта ratio (по умолчанию 5)
}

// DefaultSettings возвращает настройки по умолчанию.
// Оптимизированы для микросервисов с быстрым восстановлением.
func DefaultSettings() Settings {
	return Settings{
		MaxRequests:  1,                // В Half-Open пропускаем 1 запрос
		Interval:     60 * time.Second, // Сбрасываем счётчик каждые 60 секунд
		Timeout:      30 * time.Second, // Через 30 секунд пробуем восстановить связь
		FailureRatio: 0.5,              // Открываем при 50% ошибок
		MinRequests:  5,                // Минимум 5 запросов для принятия решения
	}
}

// Breaker — обёртка над gobreaker с логированием.
type Breaker struct {
	cb   *gobreaker.CircuitBreaker[any]
	name string
}

// New создаёт новый Circuit Breaker с настройками по умолчанию.
func New(name string) *Breaker {
	return NewWithSettings(name, DefaultSettings())
}

// NewWithSettings создаёт Circuit Breaker с пользовательскими настройками.
func NewWithSettings(name string, s Settings) *Breaker {
	cb := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        name,
		MaxRequests: s.MaxRequests,
		Interval:    s.Interval,
		Timeout:     s.Timeout,

		// ReadyToTrip определяет когда открыть breaker.
		// Открываем если доля ошибок >= FailureRatio и было >= MinRequests запросов.
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < s.MinRequests {
				return false
			}
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return failureRatio >= s.FailureRatio
		},

		// OnStateChange логирует смену состояния.
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			log := logger.With().
				Str("breaker", name).
				Str("from", from.String()).
				Str("to", to.String()).
				Logger()

			switch to {
			case gobreaker.StateOpen:
				log.Warn().Msg("Circuit Breaker ОТКРЫТ — сервис недоступен")
			case gobreaker.StateHalfOpen:
				log.Info().Msg("Circuit Breaker ПОЛУОТКРЫТ — пробуем восстановить")
			case gobreaker.StateClosed:
				log.Info().Msg("Circuit Breaker ЗАКРЫТ — сервис восстановлен")
			}
		},
	})

	return &Breaker{cb: cb, name: name}
}

// State возвращает текущее состояние breaker.
func (b *Breaker) State() gobreaker.State {
	return b.cb.State()
}

// Name возвращает имя breaker.
func (b *Breaker) Name() string {
	return b.name
}

// UnaryClientInterceptor возвращает gRPC interceptor для Circuit Breaker.
// Оборачивает каждый unary RPC вызов в Circuit Breaker.
func UnaryClientInterceptor(b *Breaker) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		var invokeErr error

		// Выполняем запрос через Circuit Breaker.
		_, cbErr := b.cb.Execute(func() (any, error) {
			invokeErr = invoker(ctx, method, req, reply, cc, opts...)
			if invokeErr != nil {
				// Только серьёзные ошибки учитываем в Circuit Breaker.
				// Бизнес-ошибки (NotFound, InvalidArgument) не открывают breaker.
				if isCircuitBreakerFailure(invokeErr) {
					return nil, invokeErr
				}
			}
			// Успех или бизнес-ошибка — для breaker это успех.
			return nil, nil
		})

		// Circuit Breaker открыт — мгновенный отказ.
		if cbErr == gobreaker.ErrOpenState {
			return status.Error(codes.Unavailable, "сервис временно недоступен (circuit breaker open)")
		}
		if cbErr == gobreaker.ErrTooManyRequests {
			return status.Error(codes.Unavailable, "слишком много запросов (circuit breaker half-open)")
		}

		// Возвращаем оригинальную ошибку gRPC (или nil).
		return invokeErr
	}
}

// isCircuitBreakerFailure определяет, должна ли ошибка учитываться в Circuit Breaker.
// Учитываем только инфраструктурные ошибки, а не бизнес-логику.
func isCircuitBreakerFailure(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		// Не gRPC ошибка — считаем сбоем.
		return true
	}

	switch st.Code() {
	case codes.Unavailable, // Сервис недоступен
		codes.DeadlineExceeded, // Таймаут
		codes.Aborted,          // Транзакция прервана
		codes.Internal,         // Внутренняя ошибка сервера
		codes.Unknown:          // Неизвестная ошибка
		return true
	default:
		// NotFound, InvalidArgument, PermissionDenied, etc. — бизнес-ошибки.
		return false
	}
}
