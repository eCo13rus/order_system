// Payment Service — микросервис обработки платежей для Saga Orchestration.
// Слушает saga.commands из Kafka, обрабатывает платежи и сохраняет reply в outbox.
// OutboxWorker отправляет reply в Kafka (saga.replies) с гарантией at-least-once.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"example.com/order-system/pkg/config"
	dbpkg "example.com/order-system/pkg/db"
	"example.com/order-system/pkg/healthcheck"
	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/metrics"
	"example.com/order-system/pkg/outbox"
	"example.com/order-system/pkg/tracing"
	"example.com/order-system/services/payment/internal/repository"
	"example.com/order-system/services/payment/internal/saga"
	"example.com/order-system/services/payment/internal/service"
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
	log := logger.With().Str("service", "payment-service").Logger()

	log.Info().
		Str("env", cfg.App.Env).
		Int("port", cfg.GRPC.PaymentService.Port).
		Msg("Запуск Payment Service")

	// === Observability: Tracing ===

	// Инициализируем distributed tracing (Jaeger)
	shutdownTracing, err := tracing.InitTracer(tracing.Config{
		ServiceName:    "payment-service",
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

	// Подключаемся к Redis
	rdb := dbpkg.ConnectRedis(cfg.Redis)
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия Redis")
		}
	}()

	// Проверяем подключение к Redis
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := rdb.Ping(ctx).Err(); err != nil {
		cancel()
		log.Fatal().Err(err).Msg("Ошибка подключения к Redis")
	}
	cancel()
	log.Info().Msg("Подключение к Redis установлено")

	// ReadinessChecker для /readyz — проверяет MySQL и Redis
	readinessCheck := healthcheck.Composite(
		func(ctx context.Context) error { return healthcheck.CheckMySQL(ctx, db) },
		func(ctx context.Context) error { return healthcheck.CheckRedis(ctx, rdb) },
	)

	// === Observability: Metrics ===

	// Запускаем HTTP сервер для Prometheus метрик
	var metricsServer *metrics.Server
	var metricsWg sync.WaitGroup
	if cfg.Metrics.Enabled {
		metricsServer = metrics.NewServer(
			cfg.Metrics.Addr(),
			"payment-service",
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

	// Создаём слои приложения (Clean Architecture)
	paymentRepo := repository.NewPaymentRepository(db)
	paymentService := service.NewPaymentService(paymentRepo, rdb)

	// Outbox Repository для записи reply (Outbox Pattern)
	outboxRepo := outbox.NewOutboxRepository(db, "payment")

	// Контекст для graceful shutdown
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	// Инициализируем Kafka компоненты
	var commandHandler *saga.CommandHandler
	var kafkaProducer *kafka.Producer
	var workersWg sync.WaitGroup // WaitGroup для ожидания завершения фоновых воркеров при shutdown

	if len(cfg.Kafka.Brokers) > 0 {
		log.Info().Strs("brokers", cfg.Kafka.Brokers).Msg("Инициализация Kafka")

		// Создаём топики если не существуют
		if err := kafka.EnsureTopics(cfg.Kafka.Brokers, kafka.DefaultSagaTopics()); err != nil {
			log.Warn().Err(err).Msg("Не удалось создать топики (возможно Kafka недоступна)")
		}

		// Создаём Producer для Outbox Worker (отправка saga.replies в Kafka)
		kafkaProducer, err = kafka.NewProducer(kafka.Config{Brokers: cfg.Kafka.Brokers})
		if err != nil {
			log.Fatal().Err(err).Msg("Ошибка создания Kafka Producer")
		}

		// Создаём Consumer для чтения saga.commands
		kafkaConsumer, err := kafka.NewConsumer(
			kafka.Config{Brokers: cfg.Kafka.Brokers},
			kafka.TopicSagaCommands,
			"payment-service-saga-consumer",
		)
		if err != nil {
			log.Fatal().Err(err).Msg("Ошибка создания Kafka Consumer")
		}

		// Устанавливаем DLQ Producer для ошибочных сообщений
		kafkaConsumer.SetDLQProducer(kafkaProducer)

		// Создаём Command Handler (использует outbox вместо прямой отправки в Kafka)
		commandHandler = saga.NewCommandHandler(kafkaConsumer, outboxRepo, paymentService)

		// WaitGroup для ожидания завершения фоновых воркеров при shutdown
		workersWg.Add(1)
		go func() {
			defer workersWg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Msg("Паника в обработчике Saga команд")
				}
			}()
			log.Info().Msg("Запуск обработчика Saga команд")
			if err := commandHandler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error().Err(err).Msg("Ошибка обработчика Saga команд")
			}
		}()

		// Запускаем Outbox Worker (читает outbox → отправляет в Kafka)
		outboxWorker := outbox.NewOutboxWorker(outboxRepo, kafkaProducer, outbox.DefaultWorkerConfig(), "payment")
		workersWg.Add(1)
		go func() {
			defer workersWg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Msg("Паника в Payment Outbox Worker")
				}
			}()
			outboxWorker.Run(ctx)
		}()

		log.Info().Msg("Payment Service Kafka Handler + Outbox Worker запущены")
	} else {
		log.Warn().Msg("Kafka не настроена — обработка Saga команд отключена")
	}

	// Ожидаем сигнал завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Получен сигнал завершения, останавливаем сервер...")

	// Отменяем контекст — останавливаем Kafka Consumer и Outbox Worker
	cancel()

	// Ждём завершения всех фоновых воркеров перед закрытием ресурсов
	workersWg.Wait()

	// Закрываем Kafka компоненты
	if commandHandler != nil {
		if err := commandHandler.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия Command Handler")
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
		metricsWg.Wait()
	}

	// Останавливаем Tracing
	if shutdownTracing != nil {
		if err := shutdownTracing(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Ошибка остановки Tracing")
		}
	}

	log.Info().Msg("Payment Service остановлен")
}
