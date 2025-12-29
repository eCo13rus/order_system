// Package domain содержит бизнес-сущности и доменные ошибки User Service.
package domain

import (
	"regexp"
	"strings"
	"time"
)

// emailRegex — регулярное выражение для валидации email.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// User представляет пользователя системы.
// Это доменная сущность без зависимостей от инфраструктуры (GORM, proto).
type User struct {
	ID        string    // Уникальный идентификатор (UUID)
	Name      string    // Имя пользователя
	Email     string    // Email пользователя (уникальный)
	Password  string    // Хеш пароля (bcrypt)
	CreatedAt time.Time // Дата создания аккаунта
	UpdatedAt time.Time // Дата последнего обновления
}

// Validate проверяет корректность полей пользователя.
// Вызывается перед созданием пользователя.
func (u *User) Validate() error {
	if err := u.ValidateEmail(); err != nil {
		return err
	}

	if err := u.ValidateName(); err != nil {
		return err
	}

	return nil
}

// ValidateEmail проверяет корректность email.
func (u *User) ValidateEmail() error {
	email := strings.TrimSpace(u.Email)
	if email == "" {
		return ErrInvalidEmail
	}

	if !emailRegex.MatchString(email) {
		return ErrInvalidEmail
	}

	return nil
}

// ValidateName проверяет, что имя пользователя не пустое.
func (u *User) ValidateName() error {
	if strings.TrimSpace(u.Name) == "" {
		return ErrEmptyName
	}
	return nil
}

// ValidatePassword проверяет требования к паролю.
// Минимум 8 символов.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return ErrWeakPassword
	}
	return nil
}

// TokenClaims содержит информацию из валидированного токена.
// Используется для передачи данных между слоями без привязки к pkg/jwt.
type TokenClaims struct {
	UserID    string    // ID пользователя
	Email     string    // Email (опционально, получается из БД)
	JTI       string    // Уникальный идентификатор токена
	IssuedAt  time.Time // Время выдачи токена
	ExpiresAt time.Time // Время истечения токена
}

// TokenPair содержит пару access и refresh токенов.
type TokenPair struct {
	AccessToken  string // JWT access token
	RefreshToken string // JWT refresh token
	ExpiresAt    int64  // Unix timestamp истечения access token
}
