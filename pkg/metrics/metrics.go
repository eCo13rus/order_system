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

// Server — HTTP сервер для экспорта метрик Prometheus.
type Server struct {
	httpServer *http.Server
	service    string
}

// NewServer создаёт новый metrics server.
// addr — адрес для прослушивания (например ":9090")
// service — имя сервиса для логирования
func NewServer(addr, service string) *Server {
	mux := http.NewServeMux()

	// /metrics — endpoint для Prometheus (он сам приходит сюда и забирает метрики)
	mux.Handle("/metrics", promhttp.Handler())

	// /health — простой health check (полезно для отладки)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		service: service,
	}
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
