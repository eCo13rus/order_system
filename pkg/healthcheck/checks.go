// Package healthcheck предоставляет функции проверки готовности сервисов.
// Используется для Kubernetes readiness probes (/readyz).
package healthcheck

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// CheckMySQL проверяет доступность MySQL через GORM.
func CheckMySQL(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("mysql: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("mysql ping: %w", err)
	}
	return nil
}

// CheckRedis проверяет доступность Redis.
func CheckRedis(ctx context.Context, rdb *redis.Client) error {
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

// Composite объединяет несколько проверок в одну.
// Возвращает первую ошибку или nil если все проверки пройдены.
func Composite(checks ...func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		for _, check := range checks {
			if err := check(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}
