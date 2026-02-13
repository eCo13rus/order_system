// Order Service — микросервис управления заказами и Saga Orchestrator.
// Предоставляет gRPC API для создания, получения, отмены заказов.
// Координирует распределённые транзакции через Saga Pattern.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"example.com/order-system/pkg/config"
	dbpkg "example.com/order-system/pkg/db"
	"example.com/order-system/pkg/healthcheck"
	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/metrics"
	"example.com/order-system/pkg/middleware"
	outboxpkg "example.com/order-system/pkg/outbox"
	"example.com/order-system/pkg/tracing"
	orderv1 "example.com/order-system/proto/order/v1"
	ordergrpc "example.com/order-system/services/order/internal/grpc"
	"example.com/order-system/services/order/internal/repository"
	"example.com/order-system/services/order/internal/saga"
	"example.com/order-system/services/order/internal/service"
)

func main() {
	// Загружаем конфигурацию
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка загрузки конфигурации: %v\n", err)
		os.Exit(1)
	}

	// Инициализируем логгер
	logger.Init(logger.Config{
		Level:  cfg.App.LogLevel,
		Pretty: cfg.App.LogPretty,
	})

	// Создаём логгер с контекстом сервиса
	log := logger.With().Str("service", "order-service").Logger()

	log.Info().
		Str("env", cfg.App.Env).
		Int("port", cfg.GRPC.OrderService.Port).
		Msg("Запуск Order Service")

	// === Observability: Tracing ===

	// Инициализируем distributed tracing (Jaeger)
	shutdownTracing, err := tracing.InitTracer(tracing.Config{
		ServiceName:    "order-service",
		JaegerEndpoint: cfg.Jaeger.OTLPEndpoint(),
		Enabled:        cfg.Jaeger.Enabled,
	})
	if err != nil {
		log.Warn().Err(err).Msg("Не удалось инициализировать tracing")
	}

	// === Подключение к зависимостям ===

	// Подключаемся к MySQL
	db, err := dbpkg.ConnectMySQL(cfg.MySQL, cfg.IsDevelopment())
	if err != nil {
		log.Fatal().Err(err).Msg("Ошибка подключения к MySQL")
	}
	log.Info().Msg("Подключение к MySQL установлено")

	// Создаём слои приложения (Clean Architecture)
	orderRepo := repository.NewOrderRepository(db)
	sagaRepo := saga.NewSagaRepository(db)
	outboxRepo := outboxpkg.NewOutboxRepository(db, "order")

	// Создаём Saga Orchestrator (если Kafka настроена)
	var orchestrator saga.Orchestrator
	var kafkaProducer *kafka.Producer
	var outboxWorker *outboxpkg.OutboxWorker
	var replyConsumer *saga.ReplyConsumer
	var timeoutWorker *saga.SagaTimeoutWorker

	if len(cfg.Kafka.Brokers) > 0 {
		log.Info().Strs("brokers", cfg.Kafka.Brokers).Msg("Инициализация Kafka для Saga")

		// Создаём топики если не существуют (idempotent)
		if err := kafka.EnsureTopics(cfg.Kafka.Brokers, kafka.DefaultSagaTopics()); err != nil {
			log.Warn().Err(err).Msg("Не удалось создать топики (возможно Kafka недоступна)")
		}

		// Создаём Kafka Producer для Outbox Worker
		var err error
		kafkaProducer, err = kafka.NewProducer(kafka.Config{Brokers: cfg.Kafka.Brokers})
		if err != nil {
			log.Fatal().Err(err).Msg("Ошибка создания Kafka Producer")
		}

		// Создаём Orchestrator (outboxRepo не нужен — записи создаются атомарно через SagaRepository)
		orchestrator = saga.NewOrchestrator(sagaRepo, orderRepo)

		// Создаём Outbox Worker
		outboxWorker = outboxpkg.NewOutboxWorker(outboxRepo, kafkaProducer, outboxpkg.DefaultWorkerConfig(), "order")

		// Создаём Reply Consumer
		kafkaConsumer, err := kafka.NewConsumer(
			kafka.Config{Brokers: cfg.Kafka.Brokers},
			kafka.TopicSagaReplies,
			"order-service-saga-consumer",
		)
		if err != nil {
			log.Fatal().Err(err).Msg("Ошибка создания Kafka Consumer")
		}
		kafkaConsumer.SetDLQProducer(kafkaProducer)
		replyConsumer = saga.NewReplyConsumer(kafkaConsumer, orchestrator)

		// Создаём Saga Timeout Worker — обнаруживает и компенсирует зависшие саги
		timeoutWorker = saga.NewSagaTimeoutWorker(sagaRepo, orchestrator, saga.DefaultTimeoutWorkerConfig())

		log.Info().Msg("Saga Orchestrator инициализирован")
	} else {
		log.Warn().Msg("Kafka не настроена — Saga Orchestrator отключен")
	}

	// ReadinessChecker для /readyz — проверяет MySQL
	readinessCheck := func(ctx context.Context) error {
		return healthcheck.CheckMySQL(ctx, db)
	}

	// === Observability: Metrics ===

	// Запускаем HTTP сервер для Prometheus метрик
	// Порт настраивается через METRICS_PORT (дефолт 9090, локально переопределяем)
	var metricsServer *metrics.Server
	var metricsWg sync.WaitGroup // WaitGroup для корректного завершения горутины Metrics Server
	if cfg.Metrics.Enabled {
		metricsServer = metrics.NewServer(
			cfg.Metrics.Addr(),
			"order-service",
			metrics.WithReadinessCheck(readinessCheck),
		)
		metricsWg.Add(1)
		go func() {
			defer metricsWg.Done()
			if err := metricsServer.Start(); err != nil {
				log.Error().Err(err).Msg("Ошибка Metrics Server")
			}
		}()
	}

	// === Инициализация бизнес-логики ===

	// Создаём OrderService с Orchestrator (может быть nil)
	orderService := service.NewOrderService(orderRepo, orchestrator)
	orderHandler := ordergrpc.NewHandler(orderService)

	// Создаём gRPC сервер с middleware
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.ChainUnaryInterceptors()...),
		grpc.ChainStreamInterceptor(middleware.ChainStreamInterceptors()...),
	)

	// Регистрируем сервис
	orderv1.RegisterOrderServiceServer(grpcServer, orderHandler)

	// Запускаем gRPC сервер
	addr := cfg.GRPC.OrderService.Addr()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("Ошибка создания listener")
	}

	// Контекст для graceful shutdown всех горутин
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Гарантируем отмену контекста при любом завершении

	// Запускаем Saga компоненты (если Kafka настроена)
	var workersWg sync.WaitGroup // WaitGroup для ожидания завершения фоновых воркеров при shutdown

	if outboxWorker != nil {
		workersWg.Add(1)
		go func() {
			defer workersWg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Msg("Паника в Outbox Worker")
				}
			}()
			log.Info().Msg("Запуск Outbox Worker")
			outboxWorker.Run(ctx)
		}()
	}

	if replyConsumer != nil {
		workersWg.Add(1)
		go func() {
			defer workersWg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Msg("Паника в Reply Consumer")
				}
			}()
			log.Info().Msg("Запуск Reply Consumer")
			if err := replyConsumer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error().Err(err).Msg("Ошибка Reply Consumer")
			}
		}()
	}

	if timeoutWorker != nil {
		workersWg.Add(1)
		go func() {
			defer workersWg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Msg("Паника в Saga Timeout Worker")
				}
			}()
			timeoutWorker.Run(ctx)
		}()
	}

	// Запускаем gRPC сервер
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("Паника в gRPC сервере")
			}
		}()
		log.Info().Str("addr", addr).Msg("gRPC сервер запущен")
		if err := grpcServer.Serve(listener); err != nil {
			log.Error().Err(err).Msg("Ошибка gRPC сервера")
		}
	}()

	// Ожидаем сигнал завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Получен сигнал завершения, останавливаем сервер...")

	// Отменяем контекст — останавливаем Outbox Worker и Reply Consumer
	cancel()

	// Ждём завершения всех фоновых воркеров перед закрытием ресурсов
	workersWg.Wait()

	// Graceful stop gRPC сервера с таймаутом 10 секунд.
	// Если за 10 секунд не завершатся текущие запросы — принудительный Stop().
	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
		log.Info().Msg("gRPC сервер остановлен корректно")
	case <-time.After(10 * time.Second):
		log.Warn().Msg("Таймаут GracefulStop, принудительная остановка gRPC")
		grpcServer.Stop()
	}

	// Закрываем Kafka компоненты
	if replyConsumer != nil {
		if err := replyConsumer.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия Reply Consumer")
		}
	}
	if kafkaProducer != nil {
		if err := kafkaProducer.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия Kafka Producer")
		}
	}

	// Закрываем подключение к MySQL
	if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
		if err := sqlDB.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия MySQL")
		}
	}

	// Останавливаем Metrics Server (если был запущен) и ждём завершения горутины
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if metricsServer != nil {
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Ошибка остановки Metrics Server")
		}
		metricsWg.Wait() // Ждём завершения горутины Metrics Server
	}

	// Останавливаем Tracing
	if shutdownTracing != nil {
		if err := shutdownTracing(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Ошибка остановки Tracing")
		}
	}

	log.Info().Msg("Order Service остановлен")
}
