// Package client содержит gRPC клиенты для взаимодействия с микросервисами.
package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/circuitbreaker"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/middleware"
	userv1 "example.com/order-system/proto/user/v1"
)

// UserClient — клиент для взаимодействия с User Service.
type UserClient struct {
	conn   *grpc.ClientConn
	client userv1.UserServiceClient
}

// UserClientConfig — конфигурация клиента.
type UserClientConfig struct {
	Addr           string                  // Адрес User Service (host:port)
	Timeout        time.Duration           // Таймаут подключения
	CircuitBreaker *circuitbreaker.Breaker // Circuit Breaker (опционально)
}

// NewUserClient создаёт новый gRPC клиент к User Service.
func NewUserClient(cfg UserClientConfig) (*UserClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}

	// gRPC опции.
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	// Добавляем Circuit Breaker interceptor если задан.
	if cfg.CircuitBreaker != nil {
		opts = append(opts, grpc.WithUnaryInterceptor(
			circuitbreaker.UnaryClientInterceptor(cfg.CircuitBreaker),
		))
	}

	// Создаём соединение через grpc.NewClient (современный API).
	conn, err := grpc.NewClient(cfg.Addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания gRPC клиента (%s): %w", cfg.Addr, err)
	}

	// Проверяем доступность сервиса с таймаутом.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	client := userv1.NewUserServiceClient(conn)

	// Пробуем пинг — если сервис недоступен, узнаем сразу.
	// ValidateToken с пустым токеном — лёгкий способ проверить связь.
	_, err = client.ValidateToken(ctx, &userv1.ValidateTokenRequest{AccessToken: ""})
	// Ошибка gRPC Unavailable означает, что сервис недоступен.
	// Другие ошибки (InvalidArgument) означают, что связь есть.
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			_ = conn.Close() // Игнорируем ошибку закрытия — уже есть ошибка подключения.
			return nil, fmt.Errorf("User Service недоступен (%s): %w", cfg.Addr, err)
		}
	}

	logger.Info().
		Str("addr", cfg.Addr).
		Msg("Подключено к User Service")

	return &UserClient{
		conn:   conn,
		client: client,
	}, nil
}

// Close закрывает соединение с User Service.
func (c *UserClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Register регистрирует нового пользователя.
func (c *UserClient) Register(ctx context.Context, email, password, name string) (string, error) {
	// Инжектим trace metadata для propagation
	ctx = middleware.InjectTraceMetadata(ctx)

	resp, err := c.client.Register(ctx, &userv1.RegisterRequest{
		Email:    email,
		Password: password,
		Name:     name,
	})
	if err != nil {
		return "", err
	}

	return resp.GetUserId(), nil
}

// Login аутентифицирует пользователя.
func (c *UserClient) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	ctx = middleware.InjectTraceMetadata(ctx)

	resp, err := c.client.Login(ctx, &userv1.LoginRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		AccessToken:  resp.GetAccessToken(),
		RefreshToken: resp.GetRefreshToken(),
		ExpiresAt:    resp.GetExpiresAt(),
	}, nil
}

// LoginResult — результат аутентификации.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
}

// Logout выход из системы (инвалидация токена).
func (c *UserClient) Logout(ctx context.Context, accessToken string) error {
	ctx = middleware.InjectTraceMetadata(ctx)

	_, err := c.client.Logout(ctx, &userv1.LogoutRequest{
		AccessToken: accessToken,
	})
	return err
}

// ValidateToken проверяет валидность токена.
// Возвращает информацию о токене или ошибку.
func (c *UserClient) ValidateToken(ctx context.Context, accessToken string) (*TokenInfo, error) {
	ctx = middleware.InjectTraceMetadata(ctx)

	resp, err := c.client.ValidateToken(ctx, &userv1.ValidateTokenRequest{
		AccessToken: accessToken,
	})
	if err != nil {
		return nil, err
	}

	return &TokenInfo{
		Valid:  resp.GetValid(),
		UserID: resp.GetUserId(),
		Email:  resp.GetEmail(),
		JTI:    resp.GetJti(),
	}, nil
}

// TokenInfo — информация о токене.
type TokenInfo struct {
	Valid  bool
	UserID string
	Email  string
	JTI    string
}

// GetUser возвращает информацию о пользователе.
func (c *UserClient) GetUser(ctx context.Context, userID string) (*User, error) {
	ctx = middleware.InjectTraceMetadata(ctx)

	resp, err := c.client.GetUser(ctx, &userv1.GetUserRequest{
		UserId: userID,
	})
	if err != nil {
		return nil, err
	}

	u := resp.GetUser()
	return &User{
		ID:        u.GetId(),
		Email:     u.GetEmail(),
		Name:      u.GetName(),
		CreatedAt: u.GetCreatedAt(),
		UpdatedAt: u.GetUpdatedAt(),
	}, nil
}

// User — информация о пользователе.
type User struct {
	ID        string
	Email     string
	Name      string
	CreatedAt int64
	UpdatedAt int64
}
