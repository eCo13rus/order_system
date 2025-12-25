// Package jwt предоставляет работу с JWT токенами на основе RS256.
// Использует асимметричную криптографию: приватный ключ для подписи,
// публичный ключ для верификации.
package jwt

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims содержит данные JWT токена.
type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`          // ID пользователя
	Role   string `json:"role,omitempty"`   // Роль пользователя (опционально)
}

// TokenPair содержит пару access и refresh токенов.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // Unix timestamp истечения access token
}

// Manager управляет созданием и валидацией JWT токенов.
// Поддерживает RS256 (асимметричная криптография).
type Manager struct {
	privateKey      *rsa.PrivateKey // Приватный ключ (только для issuer)
	publicKey       *rsa.PublicKey  // Публичный ключ (для верификации)
	blacklist       *Blacklist      // Blacklist для отзыва токенов (опционально)
	issuer          string          // Издатель токена
	accessTokenTTL  time.Duration   // Время жизни access token
	refreshTokenTTL time.Duration   // Время жизни refresh token
}

// Config содержит параметры для создания Manager.
type Config struct {
	PrivateKeyPath  string        // Путь к приватному ключу (опционально для валидаторов)
	PublicKeyPath   string        // Путь к публичному ключу (обязательно)
	Issuer          string        // Издатель токена
	AccessTokenTTL  time.Duration // Время жизни access token
	RefreshTokenTTL time.Duration // Время жизни refresh token
}

// NewManager создаёт новый менеджер JWT токенов.
// Если privateKeyPath пустой — менеджер работает только в режиме валидации.
func NewManager(cfg Config) (*Manager, error) {
	m := &Manager{
		issuer:          cfg.Issuer,
		accessTokenTTL:  cfg.AccessTokenTTL,
		refreshTokenTTL: cfg.RefreshTokenTTL,
	}

	// Загружаем публичный ключ (обязательно для всех сервисов)
	publicKey, err := LoadPublicKey(cfg.PublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки публичного ключа: %w", err)
	}
	m.publicKey = publicKey

	// Загружаем приватный ключ (только для User Service)
	if cfg.PrivateKeyPath != "" {
		privateKey, err := LoadPrivateKey(cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("ошибка загрузки приватного ключа: %w", err)
		}
		m.privateKey = privateKey
	}

	return m, nil
}

// GenerateTokenPair создаёт пару access и refresh токенов.
// Требует наличия приватного ключа (только для User Service).
func (m *Manager) GenerateTokenPair(userID, role string) (*TokenPair, error) {
	if m.privateKey == nil {
		return nil, fmt.Errorf("приватный ключ не загружен: генерация токенов недоступна")
	}

	now := time.Now()
	accessExpiry := now.Add(m.accessTokenTTL)

	// Access Token
	accessClaims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),              // jti — уникальный идентификатор токена
			Issuer:    m.issuer,                         // iss — издатель
			Subject:   userID,                           // sub — ID пользователя
			IssuedAt:  jwt.NewNumericDate(now),          // iat — время выдачи
			ExpiresAt: jwt.NewNumericDate(accessExpiry), // exp — время истечения
		},
		UserID: userID,
		Role:   role,
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(m.privateKey)
	if err != nil {
		return nil, fmt.Errorf("ошибка подписи access token: %w", err)
	}

	// Refresh Token (более долгоживущий, без role)
	refreshClaims := jwt.RegisteredClaims{
		ID:        uuid.New().String(),
		Issuer:    m.issuer,
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(m.refreshTokenTTL)),
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodRS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(m.privateKey)
	if err != nil {
		return nil, fmt.Errorf("ошибка подписи refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresAt:    accessExpiry.Unix(),
	}, nil
}

// ValidateToken проверяет токен и возвращает claims.
// Работает на любом сервисе (требует только публичный ключ).
func (m *Manager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Проверяем, что используется правильный алгоритм
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("неожиданный алгоритм подписи: %v", token.Header["alg"])
		}
		return m.publicKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("ошибка валидации токена: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("невалидные claims токена")
	}

	return claims, nil
}

// GetTokenID возвращает jti (ID токена) без полной валидации.
// Используется для проверки blacklist до полной валидации.
func (m *Manager) GetTokenID(tokenString string) (string, error) {
	// Парсим без валидации подписи (только для извлечения jti)
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return "", fmt.Errorf("ошибка парсинга токена: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return "", fmt.Errorf("невалидные claims")
	}

	return claims.ID, nil
}

// CanSign возвращает true, если менеджер может подписывать токены.
func (m *Manager) CanSign() bool {
	return m.privateKey != nil
}

// SetBlacklist устанавливает blacklist для проверки отозванных токенов.
func (m *Manager) SetBlacklist(bl *Blacklist) {
	m.blacklist = bl
}

// Blacklist возвращает blacklist (для операций Add, InvalidateUser).
func (m *Manager) Blacklist() *Blacklist {
	return m.blacklist
}

// RefreshTokenTTL возвращает время жизни refresh token.
// Используется для установки TTL при InvalidateUser.
func (m *Manager) RefreshTokenTTL() time.Duration {
	return m.refreshTokenTTL
}

// ValidateWithBlacklist проверяет токен + blacklist.
// Возвращает ошибку, если токен отозван или невалиден.
func (m *Manager) ValidateWithBlacklist(ctx context.Context, tokenString string) (*Claims, error) {
	// Сначала валидируем подпись и claims
	claims, err := m.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	// Если blacklist не настроен — возвращаем claims
	if m.blacklist == nil {
		return claims, nil
	}

	// Проверяем blacklist по jti (конкретный токен)
	blacklisted, err := m.blacklist.Check(ctx, claims.ID)
	if err != nil {
		return nil, fmt.Errorf("ошибка проверки blacklist: %w", err)
	}
	if blacklisted {
		return nil, fmt.Errorf("токен отозван")
	}

	// Проверяем инвалидацию пользователя (массовый отзыв)
	if claims.IssuedAt != nil {
		invalidated, err := m.blacklist.IsUserInvalidated(ctx, claims.Subject, claims.IssuedAt.Time)
		if err != nil {
			return nil, fmt.Errorf("ошибка проверки инвалидации пользователя: %w", err)
		}
		if invalidated {
			return nil, fmt.Errorf("все токены пользователя отозваны")
		}
	}

	return claims, nil
}

// LoadPrivateKey загружает RSA приватный ключ из PEM файла.
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения файла %s: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("не удалось декодировать PEM блок из %s", path)
	}

	// Пробуем PKCS#1 формат (RSA PRIVATE KEY)
	if block.Type == "RSA PRIVATE KEY" {
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}

	// Пробуем PKCS#8 формат (PRIVATE KEY)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга приватного ключа: %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ключ не является RSA приватным ключом")
	}

	return rsaKey, nil
}

// LoadPublicKey загружает RSA публичный ключ из PEM файла.
func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения файла %s: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("не удалось декодировать PEM блок из %s", path)
	}

	// Пробуем PKIX формат (PUBLIC KEY)
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// Пробуем PKCS#1 формат (RSA PUBLIC KEY)
		return x509.ParsePKCS1PublicKey(block.Bytes)
	}

	rsaKey, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("ключ не является RSA публичным ключом")
	}

	return rsaKey, nil
}
