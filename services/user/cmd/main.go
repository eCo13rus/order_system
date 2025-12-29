// User Service — микросервис аутентификации и управления пользователями.
// Предоставляет gRPC API для регистрации, входа, выхода и валидации токенов.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"example.com/order-system/pkg/config"
	"example.com/order-system/pkg/jwt"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/middleware"
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

	// Подключаемся к MySQL
	db, err := connectMySQL(cfg.MySQL, cfg.IsDevelopment())
	if err != nil {
		log.Fatal().Err(err).Msg("Ошибка подключения к MySQL")
	}
	log.Info().Msg("Подключение к MySQL установлено")

	// Подключаемся к Redis
	rdb := connectRedis(cfg.Redis)
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
	userService := service.NewUserService(userRepo, jwtAdapter)
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

	log.Info().Msg("User Service остановлен")
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

	// Настраиваем пул соединений
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return db, nil
}

// connectRedis создаёт клиент Redis.
func connectRedis(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr(),
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}
