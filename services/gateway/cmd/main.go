// Package main — точка входа API Gateway.
// API Gateway обеспечивает единую точку входа для REST API,
// транслирует запросы в gRPC, выполняет JWT аутентификацию и rate limiting.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/metrics"
	"example.com/order-system/pkg/tracing"
	"example.com/order-system/services/gateway/internal/client"
	"example.com/order-system/services/gateway/internal/config"
	"example.com/order-system/services/gateway/internal/handler"
	"example.com/order-system/services/gateway/internal/middleware"
)

func main() {
	// Загружаем конфигурацию
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("Ошибка загрузки конфигурации")
	}

	// Инициализируем логгер
	logger.Init(logger.Config{
		Level:  cfg.App.LogLevel,
		Pretty: cfg.App.LogPretty,
	})

	logger.Info().
		Str("service", cfg.App.Name).
		Str("env", cfg.App.Env).
		Msg("Запуск API Gateway")

	// === Observability: Metrics + Tracing ===

	// Запускаем HTTP сервер для Prometheus метрик
	// Порт настраивается через METRICS_PORT (дефолт 9090, локально переопределяем)
	var metricsServer *metrics.Server
	if cfg.Metrics.Enabled {
		metricsServer = metrics.NewServer(cfg.Metrics.Addr(), "gateway")
		go func() {
			if err := metricsServer.Start(); err != nil {
				logger.Error().Err(err).Msg("Ошибка Metrics Server")
			}
		}()
	}

	// Инициализируем distributed tracing (Jaeger)
	shutdownTracing, err := tracing.InitTracer(tracing.Config{
		ServiceName:    "gateway",
		JaegerEndpoint: cfg.Jaeger.OTLPEndpoint(),
		Enabled:        cfg.Jaeger.Enabled,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("Не удалось инициализировать tracing")
	}

	// === Инициализация зависимостей ===

	// Redis клиент
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error().Err(err).Msg("Ошибка закрытия Redis")
		}
	}()

	// Проверяем подключение к Redis
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		cancel()
		logger.Fatal().Err(err).Msg("Не удалось подключиться к Redis")
	}
	cancel()
	logger.Info().Str("addr", cfg.Redis.Addr()).Msg("Подключено к Redis")

	// gRPC клиент к User Service
	userClient, err := client.NewUserClient(client.UserClientConfig{
		Addr:    cfg.GRPC.UserServiceAddr,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("Ошибка подключения к User Service")
	}
	defer func() {
		if err := userClient.Close(); err != nil {
			logger.Error().Err(err).Msg("Ошибка закрытия User Service клиента")
		}
	}()

	// gRPC клиент к Order Service
	orderClient, err := client.NewOrderClient(client.OrderClientConfig{
		Addr:    cfg.GRPC.OrderServiceAddr,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("Ошибка подключения к Order Service")
	}
	defer func() {
		if err := orderClient.Close(); err != nil {
			logger.Error().Err(err).Msg("Ошибка закрытия Order Service клиента")
		}
	}()

	// === Инициализация middleware ===

	// Tracing middleware (Correlation ID, Trace ID)
	tracingMW := middleware.NewTracingMiddleware()

	// Rate limiting middleware
	var rateLimitMW *middleware.RateLimitMiddleware
	if cfg.RateLimit.Enabled {
		rateLimitMW = middleware.NewRateLimitMiddleware(middleware.RateLimitConfig{
			Redis:  redisClient,
			Limit:  cfg.RateLimit.RequestsLimit,
			Window: cfg.RateLimit.Window,
		})
		logger.Info().
			Int("limit", cfg.RateLimit.RequestsLimit).
			Dur("window", cfg.RateLimit.Window).
			Msg("Rate limiting включён")
	}

	// Auth middleware (JWT валидация через User Service)
	authMW := middleware.NewAuthMiddleware(userClient)

	// === Настройка роутера ===

	router := handler.NewRouter(handler.RouterConfig{
		UserClient:  userClient,
		OrderClient: orderClient,
		AuthMW:      authMW,
		RateLimitMW: rateLimitMW,
		TracingMW:   tracingMW,
		Debug:       cfg.IsDevelopment(),
	})

	// === HTTP сервер ===

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr(),
		Handler:      router.Engine(),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	// Запускаем сервер в горутине
	go func() {
		logger.Info().
			Str("addr", cfg.HTTP.Addr()).
			Msg("HTTP сервер запущен")

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Ошибка HTTP сервера")
		}
	}()

	// === Graceful Shutdown ===

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info().Msg("Получен сигнал завершения, останавливаем сервер...")

	// Даём 30 секунд на завершение текущих запросов
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error().Err(err).Msg("Ошибка при остановке сервера")
	}

	// Останавливаем Metrics Server (если был запущен)
	if metricsServer != nil {
		if err := metricsServer.Shutdown(ctx); err != nil {
			logger.Error().Err(err).Msg("Ошибка остановки Metrics Server")
		}
	}

	// Останавливаем Tracing
	if shutdownTracing != nil {
		if err := shutdownTracing(ctx); err != nil {
			logger.Error().Err(err).Msg("Ошибка остановки Tracing")
		}
	}

	logger.Info().Msg("API Gateway остановлен")
}
