// Order Service — микросервис управления заказами и Saga Orchestrator.
// Предоставляет gRPC API для создания, получения, отмены заказов.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"example.com/order-system/pkg/config"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/middleware"
	orderv1 "example.com/order-system/proto/order/v1"
	ordergrpc "example.com/order-system/services/order/internal/grpc"
	"example.com/order-system/services/order/internal/repository"
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

	// Подключаемся к MySQL
	db, err := connectMySQL(cfg.MySQL, cfg.IsDevelopment())
	if err != nil {
		log.Fatal().Err(err).Msg("Ошибка подключения к MySQL")
	}
	log.Info().Msg("Подключение к MySQL установлено")

	// Создаём слои приложения (Clean Architecture)
	orderRepo := repository.NewOrderRepository(db)
	orderService := service.NewOrderService(orderRepo)
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

	// Graceful shutdown
	go func() {
		log.Info().Str("addr", addr).Msg("gRPC сервер запущен")
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatal().Err(err).Msg("Ошибка gRPC сервера")
		}
	}()

	// Ожидаем сигнал завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Получен сигнал завершения, останавливаем сервер...")

	// Graceful stop gRPC сервера
	grpcServer.GracefulStop()

	// Закрываем подключение к MySQL
	if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
		if err := sqlDB.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия MySQL")
		}
	}

	log.Info().Msg("Order Service остановлен")
}

// connectMySQL создаёт подключение к MySQL через GORM.
func connectMySQL(cfg config.MySQLConfig, debug bool) (*gorm.DB, error) {
	// Настраиваем логгер GORM
	logLevel := gormlogger.Silent
	if debug {
		logLevel = gormlogger.Info
	}

	db, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("ошибка подключения к MySQL: %w", err)
	}

	// Проверяем подключение
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения sql.DB: %w", err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ошибка ping MySQL: %w", err)
	}

	// Настраиваем пул соединений
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return db, nil
}
