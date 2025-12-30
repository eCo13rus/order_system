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

// OrderService — интерфейс для взаимодействия с Order Service.
// Позволяет мокировать gRPC клиент в тестах.
type OrderService interface {
	// CreateOrder создаёт новый заказ.
	CreateOrder(ctx context.Context, userID, idempotencyKey string, items []client.OrderItem) (*client.CreateOrderResult, error)

	// GetOrder возвращает заказ по ID.
	GetOrder(ctx context.Context, orderID string) (*client.Order, error)

	// ListOrders возвращает список заказов пользователя.
	ListOrders(ctx context.Context, userID string, status *string, page, pageSize int) (*client.ListOrdersResult, error)

	// CancelOrder отменяет заказ.
	CancelOrder(ctx context.Context, orderID string) error
}
