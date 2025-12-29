// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"context"

	"example.com/order-system/services/gateway/internal/client"
)

// UserService — интерфейс для взаимодействия с User Service.
// Позволяет мокировать gRPC клиент в тестах.
type UserService interface {
	// Register регистрирует нового пользователя.
	Register(ctx context.Context, email, password, name string) (string, error)

	// Login аутентифицирует пользователя.
	Login(ctx context.Context, email, password string) (*client.LoginResult, error)

	// Logout выход из системы (инвалидация токена).
	Logout(ctx context.Context, accessToken string) error

	// GetUser возвращает информацию о пользователе.
	GetUser(ctx context.Context, userID string) (*client.User, error)

	// ValidateToken проверяет валидность токена.
	ValidateToken(ctx context.Context, accessToken string) (*client.TokenInfo, error)
}
