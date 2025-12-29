// Package grpc содержит gRPC обработчики для User Service.
package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
	userv1 "example.com/order-system/proto/user/v1"
	"example.com/order-system/services/user/internal/domain"
	"example.com/order-system/services/user/internal/service"
)

// Handler реализует gRPC интерфейс UserServiceServer.
type Handler struct {
	userv1.UnimplementedUserServiceServer
	userService service.UserService
}

// NewHandler создаёт новый gRPC обработчик.
func NewHandler(userService service.UserService) *Handler {
	return &Handler{
		userService: userService,
	}
}

// Register регистрирует нового пользователя.
func (h *Handler) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetEmail() == "" {
		return nil, status.Error(codes.InvalidArgument, "email обязателен")
	}
	if req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "пароль обязателен")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "имя обязательно")
	}

	// Вызов бизнес-логики
	userID, err := h.userService.Register(ctx, req.GetEmail(), req.GetPassword(), req.GetName())
	if err != nil {
		return nil, h.mapError(ctx, err, "Register")
	}

	log.Info().
		Str("user_id", userID).
		Str("email", req.GetEmail()).
		Msg("gRPC: пользователь зарегистрирован")

	return &userv1.RegisterResponse{
		UserId: userID,
	}, nil
}

// Login аутентифицирует пользователя.
func (h *Handler) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetEmail() == "" {
		return nil, status.Error(codes.InvalidArgument, "email обязателен")
	}
	if req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "пароль обязателен")
	}

	// Вызов бизнес-логики
	tokenPair, err := h.userService.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, h.mapError(ctx, err, "Login")
	}

	log.Info().
		Str("email", req.GetEmail()).
		Msg("gRPC: пользователь вошёл в систему")

	return &userv1.LoginResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
	}, nil
}

// Logout выход из системы (инвалидация токена).
func (h *Handler) Logout(ctx context.Context, req *userv1.LogoutRequest) (*userv1.LogoutResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetAccessToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "access_token обязателен")
	}

	// Вызов бизнес-логики
	if err := h.userService.Logout(ctx, req.GetAccessToken()); err != nil {
		return nil, h.mapError(ctx, err, "Logout")
	}

	log.Info().Msg("gRPC: пользователь вышел из системы")

	return &userv1.LogoutResponse{
		Success: true,
	}, nil
}

// ValidateToken проверяет валидность токена.
func (h *Handler) ValidateToken(ctx context.Context, req *userv1.ValidateTokenRequest) (*userv1.ValidateTokenResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetAccessToken() == "" {
		return &userv1.ValidateTokenResponse{
			Valid: false,
		}, nil
	}

	// Вызов бизнес-логики
	claims, err := h.userService.ValidateToken(ctx, req.GetAccessToken())
	if err != nil {
		// При ошибке валидации возвращаем valid=false, а не ошибку gRPC
		log.Debug().Err(err).Msg("gRPC: токен невалиден")
		return &userv1.ValidateTokenResponse{
			Valid: false,
		}, nil
	}

	log.Debug().
		Str("user_id", claims.UserID).
		Str("jti", claims.JTI).
		Msg("gRPC: токен валиден")

	return &userv1.ValidateTokenResponse{
		Valid:  true,
		UserId: claims.UserID,
		Email:  claims.Email,
		Jti:    claims.JTI,
	}, nil
}

// GetUser возвращает информацию о пользователе.
func (h *Handler) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id обязателен")
	}

	// Вызов бизнес-логики
	user, err := h.userService.GetUser(ctx, req.GetUserId())
	if err != nil {
		return nil, h.mapError(ctx, err, "GetUser")
	}

	log.Debug().
		Str("user_id", user.ID).
		Msg("gRPC: информация о пользователе получена")

	return &userv1.GetUserResponse{
		User: h.domainToProto(user),
	}, nil
}

// domainToProto конвертирует доменную сущность User в proto сообщение.
func (h *Handler) domainToProto(user *domain.User) *userv1.User {
	return &userv1.User{
		Id:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt.Unix(),
		UpdatedAt: user.UpdatedAt.Unix(),
	}
}

// mapError преобразует доменные ошибки в gRPC статусы.
func (h *Handler) mapError(ctx context.Context, err error, method string) error {
	log := logger.FromContext(ctx)

	// Маппинг доменных ошибок на gRPC коды
	switch {
	case errors.Is(err, domain.ErrUserNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, domain.ErrEmailExists):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, domain.ErrInvalidCredentials):
		return status.Error(codes.Unauthenticated, err.Error())

	case errors.Is(err, domain.ErrInvalidToken):
		return status.Error(codes.Unauthenticated, err.Error())

	case errors.Is(err, domain.ErrTokenRevoked):
		return status.Error(codes.Unauthenticated, err.Error())

	case errors.Is(err, domain.ErrWeakPassword):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrInvalidEmail):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrEmptyName):
		return status.Error(codes.InvalidArgument, err.Error())

	default:
		// Внутренняя ошибка сервера
		log.Error().
			Err(err).
			Str("method", method).
			Msg("gRPC: внутренняя ошибка")
		return status.Error(codes.Internal, "внутренняя ошибка сервера")
	}
}
