// Package db предоставляет общие функции подключения к базам данных.
// Используется всеми backend-сервисами (User, Order, Payment).
package db

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"example.com/order-system/pkg/config"
)

// ConnectMySQL создаёт подключение к MySQL через GORM.
// Включает PingContext для проверки соединения и настройку пула.
func ConnectMySQL(cfg config.MySQLConfig, debug bool) (*gorm.DB, error) {
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
