// User Service — микросервис аутентификации и управления пользователями.
// Предоставляет gRPC API для регистрации, входа, выхода и валидации токенов.
package main

import (
	"context"
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
	"example.com/order-system/pkg/jwt"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/metrics"
	"example.com/order-system/pkg/middleware"
	"example.com/order-system/pkg/tracing"
	userv1 "example.com/order-system/proto/user/v1"
	usergrpc "example.com/order-system/services/user/internal/grpc"
	"example.com/order-system/services/user/internal/repository"
	"example.com/order-system/services/user/internal/service"
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
	log := logger.With().Str("service", "user-service").Logger()

	log.Info().
		Str("env", cfg.App.Env).
		Int("port", cfg.GRPC.UserService.Port).
		Msg("Запуск User Service")

	// === Observability: Tracing ===

	// Инициализируем distributed tracing (Jaeger)
	shutdownTracing, err := tracing.InitTracer(tracing.Config{
		ServiceName:    "user-service",
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
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("Ошибка подключения к Redis")
	}
	log.Info().Msg("Подключение к Redis установлено")

	// ReadinessChecker для /readyz — проверяет MySQL и Redis
	readinessCheck := healthcheck.Composite(
		func(ctx context.Context) error { return healthcheck.CheckMySQL(ctx, db) },
		func(ctx context.Context) error { return healthcheck.CheckRedis(ctx, rdb) },
	)

	// === Observability: Metrics ===

	// Запускаем HTTP сервер для Prometheus метрик
	// Порт настраивается через METRICS_PORT (дефолт 9090, локально переопределяем)
	var metricsServer *metrics.Server
	var metricsWg sync.WaitGroup // WaitGroup для корректного завершения горутины Metrics Server
	if cfg.Metrics.Enabled {
		metricsServer = metrics.NewServer(
			cfg.Metrics.Addr(),
			"user-service",
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

	// Создаём JWT Manager (User Service может подписывать токены)
	jwtManager, err := jwt.NewManager(jwt.Config{
		PrivateKeyPath:  cfg.JWT.PrivateKeyPath,
		PublicKeyPath:   cfg.JWT.PublicKeyPath,
		Issuer:          cfg.JWT.Issuer,
		AccessTokenTTL:  cfg.JWT.AccessTokenTTL,
		RefreshTokenTTL: cfg.JWT.RefreshTokenTTL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Ошибка создания JWT Manager")
	}

	// Создаём blacklist и привязываем к JWT Manager
	blacklist := jwt.NewBlacklist(rdb)
	jwtManager.SetBlacklist(blacklist)
	log.Info().Msg("JWT Manager инициализирован")

	// Создаём слои приложения (Clean Architecture)
	userRepo := repository.NewUserRepository(db)
	jwtAdapter := service.NewJWTManagerAdapter(jwtManager)
	loginLimiter := service.NewLoginLimiter(rdb)
	userService := service.NewUserService(userRepo, jwtAdapter, loginLimiter)
	userHandler := usergrpc.NewHandler(userService)

	// Создаём gRPC сервер с middleware
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.ChainUnaryInterceptors()...),
		grpc.ChainStreamInterceptor(middleware.ChainStreamInterceptors()...),
	)

	// Регистрируем сервис
	userv1.RegisterUserServiceServer(grpcServer, userHandler)

	// Запускаем gRPC сервер
	addr := cfg.GRPC.UserService.Addr()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("Ошибка создания listener")
	}

	// Graceful shutdown
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

	log.Info().Msg("User Service остановлен")
}
