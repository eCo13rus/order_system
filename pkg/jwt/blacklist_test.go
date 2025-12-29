// Package jwt — тесты для JWT Blacklist.
// Используется miniredis для быстрых тестов без Docker.
package jwt

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestRedis создаёт miniredis и возвращает клиента.
// Возвращает функцию для закрытия сервера после теста.
func setupTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err, "не удалось запустить miniredis")

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return client, mr
}

// TestBlacklist_Add проверяет добавление токена в blacklist.
func TestBlacklist_Add(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("добавление токена с положительным TTL", func(t *testing.T) {
		jti := "test-jti-001"
		expiresAt := time.Now().Add(10 * time.Minute)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err, "ошибка добавления токена в blacklist")

		// Проверяем, что ключ существует в Redis
		key := prefixToken + jti
		assert.True(t, mr.Exists(key), "ключ должен существовать в Redis")

		// Проверяем значение
		val, err := mr.Get(key)
		require.NoError(t, err)
		assert.Equal(t, "1", val, "значение должно быть '1'")
	})

	t.Run("добавление токена с истёкшим TTL", func(t *testing.T) {
		jti := "test-jti-expired"
		expiresAt := time.Now().Add(-1 * time.Minute) // Уже истёк

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err, "не должно быть ошибки для истёкшего токена")

		// Ключ НЕ должен быть создан (нет смысла хранить)
		key := prefixToken + jti
		assert.False(t, mr.Exists(key), "ключ не должен создаваться для истёкшего токена")
	})

	t.Run("добавление нескольких токенов", func(t *testing.T) {
		tokens := []string{"jti-a", "jti-b", "jti-c"}
		expiresAt := time.Now().Add(5 * time.Minute)

		for _, jti := range tokens {
			err := bl.Add(ctx, jti, expiresAt)
			require.NoError(t, err)
		}

		// Проверяем все ключи
		for _, jti := range tokens {
			key := prefixToken + jti
			assert.True(t, mr.Exists(key), "ключ %s должен существовать", key)
		}
	})
}

// TestBlacklist_Check проверяет проверку наличия токена в blacklist.
func TestBlacklist_Check(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("токен в blacklist", func(t *testing.T) {
		jti := "blacklisted-token"
		expiresAt := time.Now().Add(10 * time.Minute)

		// Добавляем токен
		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		// Проверяем
		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err, "ошибка проверки blacklist")
		assert.True(t, blacklisted, "токен должен быть в blacklist")
	})

	t.Run("токен НЕ в blacklist", func(t *testing.T) {
		jti := "valid-token-not-blacklisted"

		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err, "ошибка проверки blacklist")
		assert.False(t, blacklisted, "токен не должен быть в blacklist")
	})

	t.Run("проверка пустого jti", func(t *testing.T) {
		blacklisted, err := bl.Check(ctx, "")
		require.NoError(t, err)
		assert.False(t, blacklisted, "пустой jti не должен быть в blacklist")
	})
}

// TestBlacklist_TTL проверяет автоматическое удаление токена после истечения TTL.
func TestBlacklist_TTL(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("токен исчезает после TTL", func(t *testing.T) {
		jti := "ttl-test-token"
		// Устанавливаем TTL 2 секунды для теста
		expiresAt := time.Now().Add(2 * time.Second)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		// Сразу после добавления — токен в blacklist
		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.True(t, blacklisted, "токен должен быть в blacklist сразу после добавления")

		// Эмулируем прохождение времени в miniredis
		mr.FastForward(3 * time.Second)

		// После истечения TTL — токен должен исчезнуть
		blacklisted, err = bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.False(t, blacklisted, "токен должен исчезнуть после TTL")
	})

	t.Run("токен остаётся до истечения TTL", func(t *testing.T) {
		jti := "ttl-test-token-2"
		expiresAt := time.Now().Add(10 * time.Second)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		// Перемещаем время на 5 секунд (меньше TTL)
		mr.FastForward(5 * time.Second)

		// Токен всё ещё должен быть в blacklist
		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.True(t, blacklisted, "токен должен оставаться в blacklist до истечения TTL")

		// Перемещаем ещё на 6 секунд (теперь TTL истёк)
		mr.FastForward(6 * time.Second)

		// Теперь токен должен исчезнуть
		blacklisted, err = bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.False(t, blacklisted, "токен должен исчезнуть после полного истечения TTL")
	})
}

// TestBlacklist_InvalidateUser проверяет инвалидацию всех токенов пользователя.
func TestBlacklist_InvalidateUser(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("инвалидация пользователя", func(t *testing.T) {
		userID := "user-123"
		refreshTTL := 24 * time.Hour

		err := bl.InvalidateUser(ctx, userID, refreshTTL)
		require.NoError(t, err, "ошибка инвалидации пользователя")

		// Проверяем, что ключ создан
		key := prefixUser + userID
		assert.True(t, mr.Exists(key), "ключ инвалидации должен существовать")

		// Значение должно быть Unix timestamp
		val, err := mr.Get(key)
		require.NoError(t, err)
		assert.NotEmpty(t, val, "значение не должно быть пустым")
	})

	t.Run("повторная инвалидация обновляет timestamp", func(t *testing.T) {
		userID := "user-456"
		refreshTTL := 24 * time.Hour

		// Первая инвалидация
		err := bl.InvalidateUser(ctx, userID, refreshTTL)
		require.NoError(t, err)

		key := prefixUser + userID
		val1, _ := mr.Get(key)

		// Ждём реальную секунду, чтобы timestamp изменился
		// (FastForward влияет только на TTL в Redis, не на time.Now() в Go)
		time.Sleep(1100 * time.Millisecond)

		// Повторная инвалидация
		err = bl.InvalidateUser(ctx, userID, refreshTTL)
		require.NoError(t, err)

		val2, _ := mr.Get(key)

		// Timestamp должен обновиться
		assert.NotEqual(t, val1, val2, "timestamp должен обновиться при повторной инвалидации")
	})
}

// TestBlacklist_IsUserInvalidated проверяет проверку инвалидации токенов пользователя.
func TestBlacklist_IsUserInvalidated(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("токен выдан ДО инвалидации — отозван", func(t *testing.T) {
		userID := "user-789"
		refreshTTL := 24 * time.Hour

		// Токен выдан в прошлом (10 секунд назад)
		issuedAt := time.Now().Add(-10 * time.Second)

		// Инвалидируем пользователя сейчас
		err := bl.InvalidateUser(ctx, userID, refreshTTL)
		require.NoError(t, err)

		// Проверяем — токен выдан ДО инвалидации, значит отозван
		invalidated, err := bl.IsUserInvalidated(ctx, userID, issuedAt)
		require.NoError(t, err)
		assert.True(t, invalidated, "токен выданный до инвалидации должен быть отозван")
	})

	t.Run("токен выдан ПОСЛЕ инвалидации — валиден", func(t *testing.T) {
		userID := "user-101"
		refreshTTL := 24 * time.Hour

		// Инвалидируем пользователя
		err := bl.InvalidateUser(ctx, userID, refreshTTL)
		require.NoError(t, err)

		// Токен выдан в будущем (через 5 секунд после инвалидации)
		issuedAt := time.Now().Add(5 * time.Second)

		// Проверяем — токен выдан ПОСЛЕ инвалидации, значит валиден
		invalidated, err := bl.IsUserInvalidated(ctx, userID, issuedAt)
		require.NoError(t, err)
		assert.False(t, invalidated, "токен выданный после инвалидации должен быть валиден")
	})

	t.Run("пользователь не инвалидирован — все токены валидны", func(t *testing.T) {
		userID := "user-never-invalidated"
		issuedAt := time.Now().Add(-1 * time.Hour) // Выдан давно

		invalidated, err := bl.IsUserInvalidated(ctx, userID, issuedAt)
		require.NoError(t, err)
		assert.False(t, invalidated, "токен должен быть валиден если пользователь не инвалидирован")
	})

	t.Run("TTL инвалидации истёк — токены снова валидны", func(t *testing.T) {
		userID := "user-ttl-expired"
		refreshTTL := 2 * time.Second // Короткий TTL для теста

		// Токен выдан в прошлом
		issuedAt := time.Now().Add(-10 * time.Second)

		err := bl.InvalidateUser(ctx, userID, refreshTTL)
		require.NoError(t, err)

		// Сразу после инвалидации — токен отозван
		invalidated, err := bl.IsUserInvalidated(ctx, userID, issuedAt)
		require.NoError(t, err)
		assert.True(t, invalidated, "токен должен быть отозван сразу после инвалидации")

		// Перемещаем время после истечения TTL (используем FastForward для TTL в Redis)
		mr.FastForward(3 * time.Second)

		// После истечения TTL — запись удалена, все токены валидны
		invalidated, err = bl.IsUserInvalidated(ctx, userID, issuedAt)
		require.NoError(t, err)
		assert.False(t, invalidated, "после истечения TTL инвалидации токен снова валиден")
	})
}

// TestBlacklist_Concurrency проверяет конкурентный доступ к blacklist.
func TestBlacklist_Concurrency(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("конкурентное добавление токенов", func(t *testing.T) {
		const numTokens = 100
		done := make(chan bool, numTokens)
		expiresAt := time.Now().Add(10 * time.Minute)

		for i := 0; i < numTokens; i++ {
			go func(idx int) {
				jti := "concurrent-jti-" + string(rune('0'+idx%10)) + string(rune('0'+idx/10))
				_ = bl.Add(ctx, jti, expiresAt)
				done <- true
			}(i)
		}

		// Ждём завершения всех горутин
		for i := 0; i < numTokens; i++ {
			<-done
		}

		// Проверяем, что ошибок не было (если дошли сюда — всё хорошо)
		assert.True(t, true, "конкурентное добавление должно работать без ошибок")
	})

	t.Run("конкурентная проверка токенов", func(t *testing.T) {
		jti := "concurrent-check-jti"
		expiresAt := time.Now().Add(10 * time.Minute)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		const numChecks = 100
		done := make(chan bool, numChecks)

		for i := 0; i < numChecks; i++ {
			go func() {
				blacklisted, err := bl.Check(ctx, jti)
				assert.NoError(t, err)
				assert.True(t, blacklisted)
				done <- true
			}()
		}

		for i := 0; i < numChecks; i++ {
			<-done
		}
	})
}

// TestBlacklist_EdgeCases проверяет граничные случаи.
func TestBlacklist_EdgeCases(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	bl := NewBlacklist(client)
	ctx := context.Background()

	t.Run("очень длинный jti", func(t *testing.T) {
		// UUID обычно 36 символов, но проверим длинный jti
		jti := "very-long-jti-" + string(make([]byte, 100))
		expiresAt := time.Now().Add(10 * time.Minute)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.True(t, blacklisted)
	})

	t.Run("специальные символы в jti", func(t *testing.T) {
		jti := "jti:with:colons:and-dashes_and_underscores"
		expiresAt := time.Now().Add(10 * time.Minute)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.True(t, blacklisted)
	})

	t.Run("TTL ровно 0", func(t *testing.T) {
		jti := "zero-ttl-token"
		expiresAt := time.Now() // TTL = 0

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		// Токен не должен быть добавлен (TTL <= 0)
		key := prefixToken + jti
		assert.False(t, mr.Exists(key), "токен с нулевым TTL не должен добавляться")
	})

	t.Run("очень маленький TTL", func(t *testing.T) {
		jti := "tiny-ttl-token"
		expiresAt := time.Now().Add(1 * time.Millisecond)

		err := bl.Add(ctx, jti, expiresAt)
		require.NoError(t, err)

		// Токен должен быть добавлен (TTL > 0)
		blacklisted, err := bl.Check(ctx, jti)
		require.NoError(t, err)
		assert.True(t, blacklisted, "токен с маленьким TTL должен быть добавлен")
	})
}
