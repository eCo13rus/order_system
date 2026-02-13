// Package service — LoginLimiter ограничивает количество неудачных попыток входа.
// Использует Redis INCR + EXPIRE для атомарного счётчика с TTL.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// loginAttemptsPrefix — префикс ключа Redis для счётчика попыток.
	loginAttemptsPrefix = "login_attempts:"

	// maxLoginAttempts — максимум неудачных попыток до блокировки.
	maxLoginAttempts = 5

	// lockoutDuration — время блокировки аккаунта после превышения лимита.
	lockoutDuration = 15 * time.Minute
)

// LoginLimiter — интерфейс для ограничения попыток входа.
// Позволяет мокать в тестах без Redis.
type LoginLimiter interface {
	// IsLocked проверяет, заблокирован ли аккаунт по email.
	IsLocked(ctx context.Context, email string) (bool, error)

	// RecordFailure увеличивает счётчик неудачных попыток.
	RecordFailure(ctx context.Context, email string) error

	// ResetAttempts сбрасывает счётчик после успешного входа.
	ResetAttempts(ctx context.Context, email string) error
}

// redisLoginLimiter — реализация LoginLimiter на Redis.
type redisLoginLimiter struct {
	rdb *redis.Client
}

// NewLoginLimiter создаёт LoginLimiter на базе Redis.
func NewLoginLimiter(rdb *redis.Client) LoginLimiter {
	return &redisLoginLimiter{rdb: rdb}
}

// IsLocked проверяет, превышен ли лимит попыток входа.
func (l *redisLoginLimiter) IsLocked(ctx context.Context, email string) (bool, error) {
	key := loginAttemptsPrefix + email
	val, err := l.rdb.Get(ctx, key).Int()
	if err == redis.Nil {
		return false, nil // Нет записи — не заблокирован
	}
	if err != nil {
		return false, fmt.Errorf("ошибка проверки блокировки: %w", err)
	}
	return val >= maxLoginAttempts, nil
}

// incrWithTTLScript — Lua-скрипт для атомарного INCR + EXPIRE.
// Решает race condition: если процесс упадёт между INCR и EXPIRE,
// ключ останется без TTL и пользователь будет заблокирован навечно.
// Lua-скрипт выполняется атомарно на Redis сервере.
var incrWithTTLScript = redis.NewScript(`
local val = redis.call('INCR', KEYS[1])
if val == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return val
`)

// RecordFailure атомарно увеличивает счётчик и устанавливает TTL.
// Используем Lua-скрипт для гарантии атомарности INCR + EXPIRE.
func (l *redisLoginLimiter) RecordFailure(ctx context.Context, email string) error {
	key := loginAttemptsPrefix + email

	_, err := incrWithTTLScript.Run(ctx, l.rdb, []string{key}, int(lockoutDuration.Seconds())).Result()
	if err != nil {
		return fmt.Errorf("ошибка увеличения счётчика попыток: %w", err)
	}

	return nil
}

// ResetAttempts удаляет счётчик после успешного входа.
func (l *redisLoginLimiter) ResetAttempts(ctx context.Context, email string) error {
	key := loginAttemptsPrefix + email
	if err := l.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("ошибка сброса счётчика попыток: %w", err)
	}
	return nil
}
