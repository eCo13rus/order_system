// Package metrics предоставляет Prometheus метрики для всех сервисов.
// Содержит базовые метрики (requests, latency, errors) и HTTP server для /metrics endpoint.
//
// Типы метрик в Prometheus:
//   - Counter: только растёт (запросы, ошибки) — "сколько всего произошло"
//   - Histogram: распределение значений (latency) — "как быстро работает"
//   - Gauge: текущее значение (активные соединения) — "сколько сейчас"
//
// Использование:
//
//	go metrics.StartServer(":9090", "order-service")
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"example.com/order-system/pkg/logger"
)

// =============================================================================
// Метрики — определяем что будем собирать
// =============================================================================

var (
	// RequestsTotal — счётчик всех запросов.
	// Labels позволяют фильтровать: requests_total{service="order", method="CreateOrder", status="success"}
	// PromQL пример: rate(requests_total{service="gateway"}[5m]) — RPS за 5 минут
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "requests_total",
			Help: "Общее количество запросов по сервису, методу и статусу",
		},
		[]string{"service", "method", "status"}, // Labels для фильтрации
	)

	// RequestDuration — гистограмма latency запросов.
	// Buckets: границы интервалов в секундах (5ms, 10ms, 25ms, ..., 10s)
	// PromQL пример: histogram_quantile(0.95, rate(request_duration_seconds_bucket[5m])) — p95 latency
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "request_duration_seconds",
			Help: "Время выполнения запроса в секундах",
			// Buckets оптимизированы для типичных API: от 5ms до 10s
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"service", "method"},
	)
)

// =============================================================================
// HTTP Server для /metrics endpoint
// =============================================================================

// ReadinessChecker — функция проверки готовности сервиса.
// Возвращает nil если сервис готов принимать трафик, иначе — ошибку.
type ReadinessChecker func(ctx context.Context) error

// Server — HTTP сервер для экспорта метрик Prometheus.
type Server struct {
	httpServer     *http.Server
	service        string
	readinessCheck ReadinessChecker // опциональная проверка готовности для /readyz
}

// Option — функциональная опция для настройки Server.
type Option func(*Server)

// WithReadinessCheck добавляет проверку готовности для /readyz endpoint.
// Если checker возвращает ошибку — /readyz вернёт 503 Service Unavailable.
func WithReadinessCheck(checker ReadinessChecker) Option {
	return func(s *Server) {
		s.readinessCheck = checker
	}
}

// NewServer создаёт новый metrics server.
// addr — адрес для прослушивания (например ":9090")
// service — имя сервиса для логирования
// opts — опциональные настройки (например WithReadinessCheck)
func NewServer(addr, service string, opts ...Option) *Server {
	s := &Server{
		service: service,
	}

	// Применяем опции
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()

	// /metrics — endpoint для Prometheus (он сам приходит сюда и забирает метрики)
	mux.Handle("/metrics", promhttp.Handler())

	// /health — простой health check (полезно для отладки, оставляем для совместимости)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// /healthz — liveness probe для Kubernetes
	// Возвращает 200 OK если процесс жив (сервер отвечает = процесс работает)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})

	// /readyz — readiness probe для Kubernetes
	// Возвращает 200 OK если сервис готов принимать трафик (все зависимости доступны)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Если ReadinessChecker не установлен — считаем сервис готовым
		if s.readinessCheck == nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ready"}`))
			return
		}

		// Проверяем готовность с таймаутом 5 секунд
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := s.readinessCheck(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			// Не выводим детали ошибки наружу (безопасность)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			logger.Warn().Err(err).Str("service", service).Msg("Readiness check failed")
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return s
}

// Start запускает HTTP сервер для метрик.
// Блокирующий вызов — запускать в горутине.
func (s *Server) Start() error {
	log := logger.With().Str("service", s.service).Logger()
	log.Info().Str("addr", s.httpServer.Addr).Msg("Запуск Metrics Server")

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully останавливает сервер.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// =============================================================================
// Вспомогательные функции для записи метрик
// =============================================================================

// RecordRequest записывает метрики запроса (вызывать в конце обработки).
// duration — время выполнения запроса
// method — имя метода (например "CreateOrder", "ProcessPayment")
// status — результат: "success" или "error"
func RecordRequest(service, method, status string, duration time.Duration) {
	RequestsTotal.WithLabelValues(service, method, status).Inc()
	RequestDuration.WithLabelValues(service, method).Observe(duration.Seconds())
}

// =============================================================================
// Gin Middleware для HTTP метрик
// =============================================================================

// GinMetricsMiddleware возвращает Gin middleware для сбора HTTP метрик.
// Записывает requests_total, request_duration_seconds для каждого запроса.
func GinMetricsMiddleware(service string) func(c *gin.Context) {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next() // Обрабатываем запрос

		// Определяем статус
		status := "success"
		if c.Writer.Status() >= 400 {
			status = "error"
		}

		// Записываем метрики
		RecordRequest(service, c.FullPath(), status, time.Since(start))
	}
}
