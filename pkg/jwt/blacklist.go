// Package jwt — blacklist для отзыва JWT токенов через Redis.
package jwt

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Префиксы ключей Redis
const (
	prefixToken = "jwt:blacklist:"   // jwt:blacklist:{jti}
	prefixUser  = "jwt:invalidated:" // jwt:invalidated:{userID}
)

// Blacklist управляет отозванными токенами в Redis.
type Blacklist struct {
	redis *redis.Client
}

// NewBlacklist создаёт новый blacklist.
func NewBlacklist(client *redis.Client) *Blacklist {
	return &Blacklist{redis: client}
}

// Add добавляет токен в blacklist.
// TTL ключа = время до истечения токена (автоочистка).
func (b *Blacklist) Add(ctx context.Context, jti string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil // Токен уже истёк, нет смысла добавлять
	}

	if err := b.redis.Set(ctx, prefixToken+jti, "1", ttl).Err(); err != nil {
		return fmt.Errorf("ошибка добавления токена в blacklist: %w", err)
	}
	return nil
}

// Check проверяет, находится ли токен в blacklist.
func (b *Blacklist) Check(ctx context.Context, jti string) (bool, error) {
	exists, err := b.redis.Exists(ctx, prefixToken+jti).Result()
	if err != nil {
		return false, fmt.Errorf("ошибка проверки blacklist: %w", err)
	}
	return exists > 0, nil
}

// InvalidateUser отзывает ВСЕ токены пользователя, выданные до текущего момента.
// Используется при: смене пароля, бане, "выйти со всех устройств".
func (b *Blacklist) InvalidateUser(ctx context.Context, userID string, refreshTTL time.Duration) error {
	// Сохраняем timestamp инвалидации. Все токены с iat < этого времени невалидны.
	// TTL = время жизни refresh token (самый долгоживущий)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	if err := b.redis.Set(ctx, prefixUser+userID, timestamp, refreshTTL).Err(); err != nil {
		return fmt.Errorf("ошибка инвалидации токенов пользователя: %w", err)
	}
	return nil
}

// IsUserInvalidated проверяет, был ли токен выдан до инвалидации пользователя.
// Возвращает true, если токен отозван (iat < timestamp инвалидации).
func (b *Blacklist) IsUserInvalidated(ctx context.Context, userID string, issuedAt time.Time) (bool, error) {
	val, err := b.redis.Get(ctx, prefixUser+userID).Result()
	if err == redis.Nil {
		return false, nil // Нет записи — пользователь не инвалидирован
	}
	if err != nil {
		return false, fmt.Errorf("ошибка проверки инвалидации пользователя: %w", err)
	}

	invalidatedAt, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return false, fmt.Errorf("ошибка парсинга timestamp инвалидации: %w", err)
	}

	// Токен выдан ДО инвалидации — значит отозван
	return issuedAt.Unix() < invalidatedAt, nil
}
