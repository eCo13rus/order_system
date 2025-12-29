// Package jwt — тесты для JWT Manager.
// Используется RSA ключи генерируемые в тестах и miniredis для blacklist.
package jwt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKeyPair содержит тестовые RSA ключи.
type testKeyPair struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// generateTestKeyPair генерирует пару RSA ключей для тестов.
func generateTestKeyPair(t *testing.T) *testKeyPair {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "не удалось сгенерировать RSA ключ")

	return &testKeyPair{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
	}
}

// createTestManager создаёт Manager напрямую с ключами (без загрузки из файлов).
func createTestManager(t *testing.T, keys *testKeyPair, opts ...func(*Manager)) *Manager {
	t.Helper()

	m := &Manager{
		privateKey:      keys.privateKey,
		publicKey:       keys.publicKey,
		issuer:          "test-issuer",
		accessTokenTTL:  15 * time.Minute,
		refreshTokenTTL: 24 * time.Hour,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// createValidationOnlyManager создаёт Manager только с публичным ключом (режим валидации).
func createValidationOnlyManager(t *testing.T, publicKey *rsa.PublicKey) *Manager {
	t.Helper()

	return &Manager{
		privateKey:      nil, // Нет приватного ключа — только валидация
		publicKey:       publicKey,
		issuer:          "test-issuer",
		accessTokenTTL:  15 * time.Minute,
		refreshTokenTTL: 24 * time.Hour,
	}
}

// writeKeyToTempFile записывает ключ во временный файл.
func writeKeyToTempFile(t *testing.T, keyData []byte, prefix string) string {
	t.Helper()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, prefix+".pem")

	err := os.WriteFile(path, keyData, 0600)
	require.NoError(t, err, "не удалось записать ключ в файл")

	return path
}

// encodePrivateKeyPKCS1 кодирует приватный ключ в формате PKCS#1.
func encodePrivateKeyPKCS1(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

// encodePrivateKeyPKCS8 кодирует приватный ключ в формате PKCS#8.
func encodePrivateKeyPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()

	bytes, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err, "не удалось закодировать ключ в PKCS#8")

	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: bytes,
	})
}

// encodePublicKeyPKIX кодирует публичный ключ в формате PKIX.
func encodePublicKeyPKIX(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()

	bytes, err := x509.MarshalPKIXPublicKey(key)
	require.NoError(t, err, "не удалось закодировать публичный ключ в PKIX")

	return pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: bytes,
	})
}

// encodePublicKeyPKCS1 кодирует публичный ключ в формате PKCS#1.
func encodePublicKeyPKCS1(key *rsa.PublicKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(key),
	})
}

// ==================== Тесты NewManager ====================

func TestNewManager(t *testing.T) {
	keys := generateTestKeyPair(t)

	t.Run("создание с приватным и публичным ключами", func(t *testing.T) {
		// Записываем ключи во временные файлы
		privatePath := writeKeyToTempFile(t, encodePrivateKeyPKCS1(keys.privateKey), "private")
		publicPath := writeKeyToTempFile(t, encodePublicKeyPKIX(t, keys.publicKey), "public")

		cfg := Config{
			PrivateKeyPath:  privatePath,
			PublicKeyPath:   publicPath,
			Issuer:          "test-issuer",
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
		}

		manager, err := NewManager(cfg)
		require.NoError(t, err, "ошибка создания Manager")
		require.NotNil(t, manager, "Manager не должен быть nil")

		assert.True(t, manager.CanSign(), "Manager должен уметь подписывать токены")
		assert.NotNil(t, manager.publicKey, "публичный ключ должен быть загружен")
		assert.NotNil(t, manager.privateKey, "приватный ключ должен быть загружен")
	})

	t.Run("создание только с публичным ключом (режим валидации)", func(t *testing.T) {
		publicPath := writeKeyToTempFile(t, encodePublicKeyPKIX(t, keys.publicKey), "public")

		cfg := Config{
			PrivateKeyPath:  "", // Без приватного ключа
			PublicKeyPath:   publicPath,
			Issuer:          "test-issuer",
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
		}

		manager, err := NewManager(cfg)
		require.NoError(t, err, "ошибка создания Manager в режиме валидации")
		require.NotNil(t, manager, "Manager не должен быть nil")

		assert.False(t, manager.CanSign(), "Manager НЕ должен уметь подписывать токены")
		assert.NotNil(t, manager.publicKey, "публичный ключ должен быть загружен")
		assert.Nil(t, manager.privateKey, "приватный ключ должен быть nil")
	})

	t.Run("ошибка: публичный ключ не найден", func(t *testing.T) {
		cfg := Config{
			PublicKeyPath:   "/nonexistent/path/public.pem",
			Issuer:          "test-issuer",
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
		}

		manager, err := NewManager(cfg)
		assert.Error(t, err, "должна быть ошибка при отсутствии публичного ключа")
		assert.Nil(t, manager, "Manager должен быть nil при ошибке")
		assert.Contains(t, err.Error(), "ошибка загрузки публичного ключа")
	})

	t.Run("ошибка: приватный ключ не найден", func(t *testing.T) {
		publicPath := writeKeyToTempFile(t, encodePublicKeyPKIX(t, keys.publicKey), "public")

		cfg := Config{
			PrivateKeyPath:  "/nonexistent/path/private.pem",
			PublicKeyPath:   publicPath,
			Issuer:          "test-issuer",
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
		}

		manager, err := NewManager(cfg)
		assert.Error(t, err, "должна быть ошибка при отсутствии приватного ключа")
		assert.Nil(t, manager, "Manager должен быть nil при ошибке")
		assert.Contains(t, err.Error(), "ошибка загрузки приватного ключа")
	})
}

// ==================== Тесты GenerateTokenPair ====================

func TestGenerateTokenPair(t *testing.T) {
	keys := generateTestKeyPair(t)

	t.Run("успешная генерация токенов", func(t *testing.T) {
		manager := createTestManager(t, keys)
		userID := "user-123"
		role := "admin"

		pair, err := manager.GenerateTokenPair(userID, role)
		require.NoError(t, err, "ошибка генерации токенов")
		require.NotNil(t, pair, "TokenPair не должен быть nil")

		assert.NotEmpty(t, pair.AccessToken, "AccessToken не должен быть пустым")
		assert.NotEmpty(t, pair.RefreshToken, "RefreshToken не должен быть пустым")
		assert.NotZero(t, pair.ExpiresAt, "ExpiresAt не должен быть нулём")

		// Проверяем, что ExpiresAt соответствует access token TTL
		expectedExpiry := time.Now().Add(15 * time.Minute).Unix()
		assert.InDelta(t, expectedExpiry, pair.ExpiresAt, 5, "ExpiresAt должен быть близок к ожидаемому")
	})

	t.Run("проверка claims в access token", func(t *testing.T) {
		manager := createTestManager(t, keys)
		userID := "user-456"
		role := "user"

		pair, err := manager.GenerateTokenPair(userID, role)
		require.NoError(t, err)

		// Парсим access token для проверки claims
		claims, err := manager.ValidateToken(pair.AccessToken)
		require.NoError(t, err, "ошибка валидации сгенерированного токена")

		// Проверяем RegisteredClaims
		assert.NotEmpty(t, claims.ID, "jti не должен быть пустым")
		assert.Equal(t, "test-issuer", claims.Issuer, "issuer должен совпадать")
		assert.Equal(t, userID, claims.Subject, "subject должен быть userID")
		assert.NotNil(t, claims.IssuedAt, "iat не должен быть nil")
		assert.NotNil(t, claims.ExpiresAt, "exp не должен быть nil")

		// Проверяем кастомные claims
		assert.Equal(t, userID, claims.UserID, "UserID должен совпадать")
		assert.Equal(t, role, claims.Role, "Role должен совпадать")

		// Проверяем формат jti (должен быть UUID)
		assert.Len(t, claims.ID, 36, "jti должен быть UUID (36 символов)")
	})

	t.Run("проверка claims в refresh token", func(t *testing.T) {
		manager := createTestManager(t, keys)
		userID := "user-789"

		pair, err := manager.GenerateTokenPair(userID, "admin")
		require.NoError(t, err)

		// Парсим refresh token (без роли)
		token, _, err := jwt.NewParser().ParseUnverified(pair.RefreshToken, &jwt.RegisteredClaims{})
		require.NoError(t, err, "ошибка парсинга refresh token")

		claims, ok := token.Claims.(*jwt.RegisteredClaims)
		require.True(t, ok, "claims должны быть RegisteredClaims")

		assert.NotEmpty(t, claims.ID, "jti не должен быть пустым")
		assert.Equal(t, "test-issuer", claims.Issuer, "issuer должен совпадать")
		assert.Equal(t, userID, claims.Subject, "subject должен быть userID")
		assert.NotNil(t, claims.ExpiresAt, "exp не должен быть nil")

		// Проверяем, что exp для refresh token больше, чем для access
		accessExp := time.Unix(pair.ExpiresAt, 0)
		refreshExp := claims.ExpiresAt.Time
		assert.True(t, refreshExp.After(accessExp), "refresh token должен истекать позже access token")
	})

	t.Run("уникальные jti для каждого токена", func(t *testing.T) {
		manager := createTestManager(t, keys)
		userID := "user-001"

		// Генерируем несколько пар токенов
		jtis := make(map[string]bool)
		for i := 0; i < 10; i++ {
			pair, err := manager.GenerateTokenPair(userID, "user")
			require.NoError(t, err)

			accessJti, err := manager.GetTokenID(pair.AccessToken)
			require.NoError(t, err)

			refreshJti, err := manager.GetTokenID(pair.RefreshToken)
			require.NoError(t, err)

			assert.False(t, jtis[accessJti], "access jti должен быть уникальным: %s", accessJti)
			assert.False(t, jtis[refreshJti], "refresh jti должен быть уникальным: %s", refreshJti)
			assert.NotEqual(t, accessJti, refreshJti, "access и refresh jti должны различаться")

			jtis[accessJti] = true
			jtis[refreshJti] = true
		}
	})

	t.Run("ошибка без приватного ключа", func(t *testing.T) {
		manager := createValidationOnlyManager(t, keys.publicKey)

		pair, err := manager.GenerateTokenPair("user-123", "admin")
		assert.Error(t, err, "должна быть ошибка без приватного ключа")
		assert.Nil(t, pair, "TokenPair должен быть nil")
		assert.Contains(t, err.Error(), "приватный ключ не загружен")
	})

	t.Run("пустой userID", func(t *testing.T) {
		manager := createTestManager(t, keys)

		pair, err := manager.GenerateTokenPair("", "admin")
		// Генерация должна работать даже с пустым userID (валидация на уровне сервиса)
		require.NoError(t, err)
		assert.NotNil(t, pair)
	})

	t.Run("пустая роль", func(t *testing.T) {
		manager := createTestManager(t, keys)

		pair, err := manager.GenerateTokenPair("user-123", "")
		require.NoError(t, err)

		claims, err := manager.ValidateToken(pair.AccessToken)
		require.NoError(t, err)
		assert.Empty(t, claims.Role, "Role должен быть пустым")
	})
}

// ==================== Тесты ValidateToken ====================

func TestValidateToken(t *testing.T) {
	keys := generateTestKeyPair(t)
	manager := createTestManager(t, keys)

	t.Run("валидный токен", func(t *testing.T) {
		pair, err := manager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		claims, err := manager.ValidateToken(pair.AccessToken)
		require.NoError(t, err, "ошибка валидации валидного токена")
		assert.Equal(t, "user-123", claims.UserID)
		assert.Equal(t, "admin", claims.Role)
	})

	t.Run("просроченный токен", func(t *testing.T) {
		// Создаём manager с коротким TTL
		shortTTLManager := &Manager{
			privateKey:      keys.privateKey,
			publicKey:       keys.publicKey,
			issuer:          "test-issuer",
			accessTokenTTL:  -1 * time.Hour, // Уже истёк
			refreshTokenTTL: 24 * time.Hour,
		}

		pair, err := shortTTLManager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		claims, err := manager.ValidateToken(pair.AccessToken)
		assert.Error(t, err, "должна быть ошибка для просроченного токена")
		assert.Nil(t, claims)
		assert.Contains(t, err.Error(), "ошибка валидации токена")
	})

	t.Run("невалидная подпись (другой ключ)", func(t *testing.T) {
		// Генерируем токен другими ключами
		otherKeys := generateTestKeyPair(t)
		otherManager := createTestManager(t, otherKeys)

		pair, err := otherManager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		// Пытаемся валидировать токен подписанный другим ключом
		claims, err := manager.ValidateToken(pair.AccessToken)
		assert.Error(t, err, "должна быть ошибка для токена с другой подписью")
		assert.Nil(t, claims)
	})

	t.Run("malformed токен", func(t *testing.T) {
		testCases := []struct {
			name  string
			token string
		}{
			{"пустой токен", ""},
			{"случайная строка", "not-a-valid-jwt-token"},
			{"неполный JWT", "eyJhbGciOiJSUzI1NiJ9"},
			{"два сегмента", "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ"},
			{"невалидный base64", "not.valid.base64!!!"},
			{"некорректный JSON в payload", "eyJhbGciOiJSUzI1NiJ9.bm90LWpzb24.signature"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				claims, err := manager.ValidateToken(tc.token)
				assert.Error(t, err, "должна быть ошибка для malformed токена")
				assert.Nil(t, claims)
			})
		}
	})

	t.Run("токен с неправильным алгоритмом (HS256)", func(t *testing.T) {
		// Создаём токен с HS256 (симметричный алгоритм)
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-123",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		tokenString, err := token.SignedString([]byte("secret"))
		require.NoError(t, err)

		claims, err := manager.ValidateToken(tokenString)
		assert.Error(t, err, "должна быть ошибка для токена с неправильным алгоритмом")
		assert.Nil(t, claims)
		assert.Contains(t, err.Error(), "неожиданный алгоритм подписи")
	})

	t.Run("валидация refresh токена", func(t *testing.T) {
		pair, err := manager.GenerateTokenPair("user-456", "moderator")
		require.NoError(t, err)

		// Refresh token тоже можно валидировать как access (структура claims немного другая)
		// Но ValidateToken ожидает Claims с UserID
		claims, err := manager.ValidateToken(pair.RefreshToken)
		// Refresh token не имеет UserID в claims, но парсится корректно
		require.NoError(t, err)
		assert.Empty(t, claims.UserID, "в refresh token нет UserID")
		assert.Equal(t, "user-456", claims.Subject, "Subject должен быть userID")
	})
}

// ==================== Тесты ValidateWithBlacklist ====================

func TestValidateWithBlacklist(t *testing.T) {
	keys := generateTestKeyPair(t)

	t.Run("токен НЕ в blacklist", func(t *testing.T) {
		client, mr := setupTestRedis(t)
		defer mr.Close()

		manager := createTestManager(t, keys)
		manager.SetBlacklist(NewBlacklist(client))

		pair, err := manager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		ctx := context.Background()
		claims, err := manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		require.NoError(t, err, "токен не в blacklist должен быть валидным")
		assert.Equal(t, "user-123", claims.UserID)
	})

	t.Run("токен в blacklist", func(t *testing.T) {
		client, mr := setupTestRedis(t)
		defer mr.Close()

		manager := createTestManager(t, keys)
		blacklist := NewBlacklist(client)
		manager.SetBlacklist(blacklist)

		pair, err := manager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		// Получаем jti и добавляем в blacklist
		jti, err := manager.GetTokenID(pair.AccessToken)
		require.NoError(t, err)

		ctx := context.Background()
		err = blacklist.Add(ctx, jti, time.Now().Add(time.Hour))
		require.NoError(t, err)

		// Теперь токен должен быть отклонён
		claims, err := manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		assert.Error(t, err, "токен в blacklist должен быть отклонён")
		assert.Nil(t, claims)
		assert.Contains(t, err.Error(), "токен отозван")
	})

	t.Run("пользователь инвалидирован", func(t *testing.T) {
		client, mr := setupTestRedis(t)
		defer mr.Close()

		manager := createTestManager(t, keys)
		blacklist := NewBlacklist(client)
		manager.SetBlacklist(blacklist)

		// Генерируем токен
		pair, err := manager.GenerateTokenPair("user-789", "admin")
		require.NoError(t, err)

		ctx := context.Background()

		// Инвалидируем пользователя ПОСЛЕ генерации токена
		// Ждём 1.1 секунды, так как JWT timestamps имеют секундную точность
		time.Sleep(1100 * time.Millisecond)
		err = blacklist.InvalidateUser(ctx, "user-789", 24*time.Hour)
		require.NoError(t, err)

		// Токен должен быть отклонён
		claims, err := manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		assert.Error(t, err, "токен инвалидированного пользователя должен быть отклонён")
		assert.Nil(t, claims)
		if err != nil {
			assert.Contains(t, err.Error(), "все токены пользователя отозваны")
		}
	})

	t.Run("новый токен после инвалидации валиден", func(t *testing.T) {
		client, mr := setupTestRedis(t)
		defer mr.Close()

		manager := createTestManager(t, keys)
		blacklist := NewBlacklist(client)
		manager.SetBlacklist(blacklist)

		ctx := context.Background()

		// Инвалидируем пользователя
		err := blacklist.InvalidateUser(ctx, "user-101", 24*time.Hour)
		require.NoError(t, err)

		// Ждём 1.1 секунды, чтобы новый токен имел iat после инвалидации
		// (JWT timestamps имеют секундную точность)
		time.Sleep(1100 * time.Millisecond)

		// Генерируем новый токен ПОСЛЕ инвалидации
		pair, err := manager.GenerateTokenPair("user-101", "admin")
		require.NoError(t, err)

		// Новый токен должен быть валиден
		claims, err := manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		require.NoError(t, err, "новый токен после инвалидации должен быть валиден")
		assert.Equal(t, "user-101", claims.UserID)
	})

	t.Run("без blacklist — обычная валидация", func(t *testing.T) {
		manager := createTestManager(t, keys)
		// Не устанавливаем blacklist

		pair, err := manager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		ctx := context.Background()
		claims, err := manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		require.NoError(t, err, "без blacklist должна работать обычная валидация")
		assert.Equal(t, "user-123", claims.UserID)
	})

	t.Run("невалидный токен не проверяется в blacklist", func(t *testing.T) {
		client, mr := setupTestRedis(t)
		defer mr.Close()

		manager := createTestManager(t, keys)
		manager.SetBlacklist(NewBlacklist(client))

		ctx := context.Background()
		claims, err := manager.ValidateWithBlacklist(ctx, "invalid-token")
		assert.Error(t, err, "невалидный токен должен быть отклонён")
		assert.Nil(t, claims)
		// Ошибка должна быть о валидации, не о blacklist
		assert.Contains(t, err.Error(), "ошибка валидации токена")
	})
}

// ==================== Тесты GetTokenID ====================

func TestGetTokenID(t *testing.T) {
	keys := generateTestKeyPair(t)
	manager := createTestManager(t, keys)

	t.Run("извлечение jti из валидного токена", func(t *testing.T) {
		pair, err := manager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		jti, err := manager.GetTokenID(pair.AccessToken)
		require.NoError(t, err, "ошибка извлечения jti")
		assert.NotEmpty(t, jti, "jti не должен быть пустым")
		assert.Len(t, jti, 36, "jti должен быть UUID")
	})

	t.Run("jti совпадает с claims.ID", func(t *testing.T) {
		pair, err := manager.GenerateTokenPair("user-456", "user")
		require.NoError(t, err)

		// Получаем jti через GetTokenID
		jti, err := manager.GetTokenID(pair.AccessToken)
		require.NoError(t, err)

		// Получаем jti через ValidateToken
		claims, err := manager.ValidateToken(pair.AccessToken)
		require.NoError(t, err)

		assert.Equal(t, claims.ID, jti, "jti должен совпадать")
	})

	t.Run("извлечение без валидации подписи", func(t *testing.T) {
		// Генерируем токен другими ключами
		otherKeys := generateTestKeyPair(t)
		otherManager := createTestManager(t, otherKeys)

		pair, err := otherManager.GenerateTokenPair("user-123", "admin")
		require.NoError(t, err)

		// GetTokenID должен работать даже с токеном, подписанным другим ключом
		jti, err := manager.GetTokenID(pair.AccessToken)
		require.NoError(t, err, "GetTokenID не должен проверять подпись")
		assert.NotEmpty(t, jti)
	})

	t.Run("malformed токен", func(t *testing.T) {
		testCases := []struct {
			name  string
			token string
		}{
			{"пустой токен", ""},
			{"случайная строка", "random-string"},
			{"невалидный base64", "not.valid.base64!!!"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				jti, err := manager.GetTokenID(tc.token)
				assert.Error(t, err, "должна быть ошибка для malformed токена")
				assert.Empty(t, jti)
			})
		}
	})

	t.Run("токен без jti", func(t *testing.T) {
		// Создаём токен без jti
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"sub": "user-123",
			"exp": time.Now().Add(time.Hour).Unix(),
			// jti не устанавливаем
		})
		tokenString, err := token.SignedString(keys.privateKey)
		require.NoError(t, err)

		jti, err := manager.GetTokenID(tokenString)
		require.NoError(t, err)
		assert.Empty(t, jti, "jti должен быть пустым если не задан")
	})
}

// ==================== Тесты CanSign ====================

func TestCanSign(t *testing.T) {
	keys := generateTestKeyPair(t)

	t.Run("true с приватным ключом", func(t *testing.T) {
		manager := createTestManager(t, keys)
		assert.True(t, manager.CanSign())
	})

	t.Run("false без приватного ключа", func(t *testing.T) {
		manager := createValidationOnlyManager(t, keys.publicKey)
		assert.False(t, manager.CanSign())
	})
}

// ==================== Тесты LoadPrivateKey ====================

func TestLoadPrivateKey(t *testing.T) {
	keys := generateTestKeyPair(t)

	t.Run("загрузка PKCS#1 формата", func(t *testing.T) {
		data := encodePrivateKeyPKCS1(keys.privateKey)
		path := writeKeyToTempFile(t, data, "private-pkcs1")

		loadedKey, err := LoadPrivateKey(path)
		require.NoError(t, err, "ошибка загрузки PKCS#1 ключа")
		require.NotNil(t, loadedKey)

		// Проверяем, что ключ работает
		assert.Equal(t, keys.privateKey.N, loadedKey.N, "модуль ключа должен совпадать")
	})

	t.Run("загрузка PKCS#8 формата", func(t *testing.T) {
		data := encodePrivateKeyPKCS8(t, keys.privateKey)
		path := writeKeyToTempFile(t, data, "private-pkcs8")

		loadedKey, err := LoadPrivateKey(path)
		require.NoError(t, err, "ошибка загрузки PKCS#8 ключа")
		require.NotNil(t, loadedKey)

		assert.Equal(t, keys.privateKey.N, loadedKey.N, "модуль ключа должен совпадать")
	})

	t.Run("ошибка: файл не существует", func(t *testing.T) {
		key, err := LoadPrivateKey("/nonexistent/path/private.pem")
		assert.Error(t, err)
		assert.Nil(t, key)
		assert.Contains(t, err.Error(), "ошибка чтения файла")
	})

	t.Run("ошибка: невалидный PEM", func(t *testing.T) {
		path := writeKeyToTempFile(t, []byte("not a valid pem"), "invalid")

		key, err := LoadPrivateKey(path)
		assert.Error(t, err)
		assert.Nil(t, key)
		assert.Contains(t, err.Error(), "не удалось декодировать PEM блок")
	})

	t.Run("ошибка: публичный ключ вместо приватного", func(t *testing.T) {
		data := encodePublicKeyPKIX(t, keys.publicKey)
		path := writeKeyToTempFile(t, data, "public-instead")

		key, err := LoadPrivateKey(path)
		assert.Error(t, err)
		assert.Nil(t, key)
	})
}

// ==================== Тесты LoadPublicKey ====================

func TestLoadPublicKey(t *testing.T) {
	keys := generateTestKeyPair(t)

	t.Run("загрузка PKIX формата", func(t *testing.T) {
		data := encodePublicKeyPKIX(t, keys.publicKey)
		path := writeKeyToTempFile(t, data, "public-pkix")

		loadedKey, err := LoadPublicKey(path)
		require.NoError(t, err, "ошибка загрузки PKIX ключа")
		require.NotNil(t, loadedKey)

		assert.Equal(t, keys.publicKey.N, loadedKey.N, "модуль ключа должен совпадать")
	})

	t.Run("загрузка PKCS#1 формата", func(t *testing.T) {
		data := encodePublicKeyPKCS1(keys.publicKey)
		path := writeKeyToTempFile(t, data, "public-pkcs1")

		loadedKey, err := LoadPublicKey(path)
		require.NoError(t, err, "ошибка загрузки PKCS#1 публичного ключа")
		require.NotNil(t, loadedKey)

		assert.Equal(t, keys.publicKey.N, loadedKey.N, "модуль ключа должен совпадать")
	})

	t.Run("ошибка: файл не существует", func(t *testing.T) {
		key, err := LoadPublicKey("/nonexistent/path/public.pem")
		assert.Error(t, err)
		assert.Nil(t, key)
		assert.Contains(t, err.Error(), "ошибка чтения файла")
	})

	t.Run("ошибка: невалидный PEM", func(t *testing.T) {
		path := writeKeyToTempFile(t, []byte("not a valid pem content"), "invalid-pem")

		key, err := LoadPublicKey(path)
		assert.Error(t, err)
		assert.Nil(t, key)
		assert.Contains(t, err.Error(), "не удалось декодировать PEM блок")
	})

	t.Run("ошибка: приватный ключ вместо публичного", func(t *testing.T) {
		data := encodePrivateKeyPKCS1(keys.privateKey)
		path := writeKeyToTempFile(t, data, "private-instead")

		key, err := LoadPublicKey(path)
		assert.Error(t, err)
		assert.Nil(t, key)
	})
}

// ==================== Тесты вспомогательных методов ====================

func TestSetBlacklist(t *testing.T) {
	keys := generateTestKeyPair(t)
	manager := createTestManager(t, keys)

	assert.Nil(t, manager.Blacklist(), "blacklist должен быть nil по умолчанию")

	client, mr := setupTestRedis(t)
	defer mr.Close()

	blacklist := NewBlacklist(client)
	manager.SetBlacklist(blacklist)

	assert.NotNil(t, manager.Blacklist(), "blacklist должен быть установлен")
	assert.Equal(t, blacklist, manager.Blacklist())
}

func TestRefreshTokenTTL(t *testing.T) {
	keys := generateTestKeyPair(t)

	expectedTTL := 7 * 24 * time.Hour
	manager := &Manager{
		privateKey:      keys.privateKey,
		publicKey:       keys.publicKey,
		issuer:          "test-issuer",
		accessTokenTTL:  15 * time.Minute,
		refreshTokenTTL: expectedTTL,
	}

	assert.Equal(t, expectedTTL, manager.RefreshTokenTTL())
}

// ==================== Интеграционные тесты ====================

func TestTokenLifecycle(t *testing.T) {
	// Полный цикл: генерация -> валидация -> blacklist -> отказ
	keys := generateTestKeyPair(t)
	client, mr := setupTestRedis(t)
	defer mr.Close()

	manager := createTestManager(t, keys)
	blacklist := NewBlacklist(client)
	manager.SetBlacklist(blacklist)

	ctx := context.Background()
	userID := "user-lifecycle"

	t.Run("полный цикл жизни токена", func(t *testing.T) {
		// 1. Генерируем токены
		pair, err := manager.GenerateTokenPair(userID, "admin")
		require.NoError(t, err)

		// 2. Валидируем — должен быть валиден
		claims, err := manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)

		// 3. Получаем jti
		jti, err := manager.GetTokenID(pair.AccessToken)
		require.NoError(t, err)

		// 4. Добавляем в blacklist (logout)
		err = blacklist.Add(ctx, jti, time.Now().Add(time.Hour))
		require.NoError(t, err)

		// 5. Валидируем — должен быть отклонён
		claims, err = manager.ValidateWithBlacklist(ctx, pair.AccessToken)
		assert.Error(t, err)
		assert.Nil(t, claims)
		assert.Contains(t, err.Error(), "токен отозван")

		// 6. Новый токен должен работать
		newPair, err := manager.GenerateTokenPair(userID, "admin")
		require.NoError(t, err)

		claims, err = manager.ValidateWithBlacklist(ctx, newPair.AccessToken)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)
	})
}

func TestMultipleServicesScenario(t *testing.T) {
	// Симуляция: User Service генерирует, API Gateway валидирует
	keys := generateTestKeyPair(t)

	t.Run("User Service генерирует, API Gateway валидирует", func(t *testing.T) {
		// User Service (с приватным ключом)
		userService := createTestManager(t, keys)
		assert.True(t, userService.CanSign())

		// API Gateway (только публичный ключ)
		apiGateway := createValidationOnlyManager(t, keys.publicKey)
		assert.False(t, apiGateway.CanSign())

		// User Service генерирует токен
		pair, err := userService.GenerateTokenPair("user-multi", "admin")
		require.NoError(t, err)

		// API Gateway валидирует токен
		claims, err := apiGateway.ValidateToken(pair.AccessToken)
		require.NoError(t, err, "API Gateway должен успешно валидировать токен")
		assert.Equal(t, "user-multi", claims.UserID)

		// API Gateway не может генерировать токены
		_, err = apiGateway.GenerateTokenPair("user-123", "admin")
		assert.Error(t, err, "API Gateway не должен генерировать токены")
	})
}
