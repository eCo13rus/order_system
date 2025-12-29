// Package grpc содержит unit тесты для gRPC Handler User Service.
package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userv1 "example.com/order-system/proto/user/v1"
	"example.com/order-system/services/user/internal/domain"
	"example.com/order-system/services/user/internal/service"
)

// =====================================
// Мок для UserService
// =====================================

type MockUserService struct {
	mock.Mock
}

var _ service.UserService = (*MockUserService)(nil)

func (m *MockUserService) Register(ctx context.Context, email, password, name string) (string, error) {
	args := m.Called(ctx, email, password, name)
	return args.String(0), args.Error(1)
}

func (m *MockUserService) Login(ctx context.Context, email, password string) (*domain.TokenPair, error) {
	args := m.Called(ctx, email, password)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.TokenPair), args.Error(1)
}

func (m *MockUserService) Logout(ctx context.Context, accessToken string) error {
	return m.Called(ctx, accessToken).Error(0)
}

func (m *MockUserService) ValidateToken(ctx context.Context, accessToken string) (*domain.TokenClaims, error) {
	args := m.Called(ctx, accessToken)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.TokenClaims), args.Error(1)
}

func (m *MockUserService) GetUser(ctx context.Context, userID string) (*domain.User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

// =====================================
// Тесты Register
// =====================================

func TestRegister(t *testing.T) {
	tests := []struct {
		name         string
		request      *userv1.RegisterRequest
		mockSetup    func(*MockUserService)
		expectedCode codes.Code
		expectedMsg  string
		checkResp    func(t *testing.T, resp *userv1.RegisterResponse)
	}{
		{
			name: "успешная регистрация",
			request: &userv1.RegisterRequest{
				Email:    "test@example.com",
				Password: "securePass123",
				Name:     "Иван Петров",
			},
			mockSetup: func(m *MockUserService) {
				m.On("Register", mock.Anything, "test@example.com", "securePass123", "Иван Петров").
					Return("user-uuid-123", nil)
			},
			expectedCode: codes.OK,
			checkResp: func(t *testing.T, resp *userv1.RegisterResponse) {
				assert.Equal(t, "user-uuid-123", resp.GetUserId())
			},
		},
		{
			name:         "пустой email",
			request:      &userv1.RegisterRequest{Email: "", Password: "pass123", Name: "Test"},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "email обязателен",
		},
		{
			name:         "пустой пароль",
			request:      &userv1.RegisterRequest{Email: "test@example.com", Password: "", Name: "Test"},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "пароль обязателен",
		},
		{
			name:         "пустое имя",
			request:      &userv1.RegisterRequest{Email: "test@example.com", Password: "pass123", Name: ""},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "имя обязательно",
		},
		{
			name: "email уже существует",
			request: &userv1.RegisterRequest{
				Email:    "existing@example.com",
				Password: "pass123",
				Name:     "Test",
			},
			mockSetup: func(m *MockUserService) {
				m.On("Register", mock.Anything, "existing@example.com", "pass123", "Test").
					Return("", domain.ErrEmailExists)
			},
			expectedCode: codes.AlreadyExists,
		},
		{
			name: "слабый пароль",
			request: &userv1.RegisterRequest{
				Email:    "test@example.com",
				Password: "weak",
				Name:     "Test",
			},
			mockSetup: func(m *MockUserService) {
				m.On("Register", mock.Anything, "test@example.com", "weak", "Test").
					Return("", domain.ErrWeakPassword)
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "внутренняя ошибка",
			request: &userv1.RegisterRequest{
				Email:    "test@example.com",
				Password: "pass123",
				Name:     "Test",
			},
			mockSetup: func(m *MockUserService) {
				m.On("Register", mock.Anything, "test@example.com", "pass123", "Test").
					Return("", errors.New("db error"))
			},
			expectedCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockUserService)
			tt.mockSetup(mockService)
			handler := NewHandler(mockService)

			resp, err := handler.Register(context.Background(), tt.request)

			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
				if tt.checkResp != nil {
					tt.checkResp(t, resp)
				}
			} else {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, st.Code())
				if tt.expectedMsg != "" {
					assert.Contains(t, st.Message(), tt.expectedMsg)
				}
			}
			mockService.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты Login
// =====================================

func TestLogin(t *testing.T) {
	tokenPair := &domain.TokenPair{
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-456",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}

	tests := []struct {
		name         string
		request      *userv1.LoginRequest
		mockSetup    func(*MockUserService)
		expectedCode codes.Code
		expectedMsg  string
		checkResp    func(t *testing.T, resp *userv1.LoginResponse)
	}{
		{
			name:    "успешный вход",
			request: &userv1.LoginRequest{Email: "test@example.com", Password: "securePass123"},
			mockSetup: func(m *MockUserService) {
				m.On("Login", mock.Anything, "test@example.com", "securePass123").Return(tokenPair, nil)
			},
			expectedCode: codes.OK,
			checkResp: func(t *testing.T, resp *userv1.LoginResponse) {
				assert.Equal(t, tokenPair.AccessToken, resp.GetAccessToken())
				assert.Equal(t, tokenPair.RefreshToken, resp.GetRefreshToken())
			},
		},
		{
			name:         "пустой email",
			request:      &userv1.LoginRequest{Email: "", Password: "pass123"},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "email обязателен",
		},
		{
			name:         "пустой пароль",
			request:      &userv1.LoginRequest{Email: "test@example.com", Password: ""},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "пароль обязателен",
		},
		{
			name:    "неверные учётные данные",
			request: &userv1.LoginRequest{Email: "test@example.com", Password: "wrong"},
			mockSetup: func(m *MockUserService) {
				m.On("Login", mock.Anything, "test@example.com", "wrong").Return(nil, domain.ErrInvalidCredentials)
			},
			expectedCode: codes.Unauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockUserService)
			tt.mockSetup(mockService)
			handler := NewHandler(mockService)

			resp, err := handler.Login(context.Background(), tt.request)

			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
				if tt.checkResp != nil {
					tt.checkResp(t, resp)
				}
			} else {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, st.Code())
				if tt.expectedMsg != "" {
					assert.Contains(t, st.Message(), tt.expectedMsg)
				}
			}
			mockService.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты Logout
// =====================================

func TestLogout(t *testing.T) {
	tests := []struct {
		name         string
		request      *userv1.LogoutRequest
		mockSetup    func(*MockUserService)
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:    "успешный logout",
			request: &userv1.LogoutRequest{AccessToken: "valid-token"},
			mockSetup: func(m *MockUserService) {
				m.On("Logout", mock.Anything, "valid-token").Return(nil)
			},
			expectedCode: codes.OK,
		},
		{
			name:         "пустой токен",
			request:      &userv1.LogoutRequest{AccessToken: ""},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "access_token обязателен",
		},
		{
			name:    "невалидный токен",
			request: &userv1.LogoutRequest{AccessToken: "invalid-token"},
			mockSetup: func(m *MockUserService) {
				m.On("Logout", mock.Anything, "invalid-token").Return(domain.ErrInvalidToken)
			},
			expectedCode: codes.Unauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockUserService)
			tt.mockSetup(mockService)
			handler := NewHandler(mockService)

			resp, err := handler.Logout(context.Background(), tt.request)

			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
				assert.True(t, resp.GetSuccess())
			} else {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, st.Code())
				if tt.expectedMsg != "" {
					assert.Contains(t, st.Message(), tt.expectedMsg)
				}
			}
			mockService.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты ValidateToken
// =====================================

func TestValidateToken(t *testing.T) {
	now := time.Now()
	validClaims := &domain.TokenClaims{
		UserID:    "user-123",
		Email:     "test@example.com",
		JTI:       "jti-456",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}

	tests := []struct {
		name      string
		request   *userv1.ValidateTokenRequest
		mockSetup func(*MockUserService)
		checkResp func(t *testing.T, resp *userv1.ValidateTokenResponse)
	}{
		{
			name:    "успешная валидация",
			request: &userv1.ValidateTokenRequest{AccessToken: "valid-token"},
			mockSetup: func(m *MockUserService) {
				m.On("ValidateToken", mock.Anything, "valid-token").Return(validClaims, nil)
			},
			checkResp: func(t *testing.T, resp *userv1.ValidateTokenResponse) {
				assert.True(t, resp.GetValid())
				assert.Equal(t, validClaims.UserID, resp.GetUserId())
				assert.Equal(t, validClaims.Email, resp.GetEmail())
				assert.Equal(t, validClaims.JTI, resp.GetJti())
			},
		},
		{
			name:      "пустой токен",
			request:   &userv1.ValidateTokenRequest{AccessToken: ""},
			mockSetup: func(m *MockUserService) {},
			checkResp: func(t *testing.T, resp *userv1.ValidateTokenResponse) {
				assert.False(t, resp.GetValid())
			},
		},
		{
			name:    "невалидный токен",
			request: &userv1.ValidateTokenRequest{AccessToken: "invalid-token"},
			mockSetup: func(m *MockUserService) {
				m.On("ValidateToken", mock.Anything, "invalid-token").Return(nil, domain.ErrInvalidToken)
			},
			checkResp: func(t *testing.T, resp *userv1.ValidateTokenResponse) {
				assert.False(t, resp.GetValid())
			},
		},
		{
			name:    "отозванный токен",
			request: &userv1.ValidateTokenRequest{AccessToken: "revoked-token"},
			mockSetup: func(m *MockUserService) {
				m.On("ValidateToken", mock.Anything, "revoked-token").Return(nil, domain.ErrTokenRevoked)
			},
			checkResp: func(t *testing.T, resp *userv1.ValidateTokenResponse) {
				assert.False(t, resp.GetValid())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockUserService)
			tt.mockSetup(mockService)
			handler := NewHandler(mockService)

			resp, err := handler.ValidateToken(context.Background(), tt.request)

			require.NoError(t, err) // ValidateToken не возвращает gRPC ошибки
			if tt.checkResp != nil {
				tt.checkResp(t, resp)
			}
			mockService.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты GetUser
// =====================================

func TestGetUser(t *testing.T) {
	now := time.Now()
	validUser := &domain.User{
		ID:        "user-123",
		Email:     "test@example.com",
		Name:      "Тест Пользователь",
		CreatedAt: now,
		UpdatedAt: now,
	}

	tests := []struct {
		name         string
		request      *userv1.GetUserRequest
		mockSetup    func(*MockUserService)
		expectedCode codes.Code
		expectedMsg  string
		checkResp    func(t *testing.T, resp *userv1.GetUserResponse)
	}{
		{
			name:    "успешное получение",
			request: &userv1.GetUserRequest{UserId: "user-123"},
			mockSetup: func(m *MockUserService) {
				m.On("GetUser", mock.Anything, "user-123").Return(validUser, nil)
			},
			expectedCode: codes.OK,
			checkResp: func(t *testing.T, resp *userv1.GetUserResponse) {
				assert.Equal(t, validUser.ID, resp.GetUser().GetId())
				assert.Equal(t, validUser.Email, resp.GetUser().GetEmail())
				assert.Equal(t, validUser.Name, resp.GetUser().GetName())
			},
		},
		{
			name:         "пустой user_id",
			request:      &userv1.GetUserRequest{UserId: ""},
			mockSetup:    func(m *MockUserService) {},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "user_id обязателен",
		},
		{
			name:    "пользователь не найден",
			request: &userv1.GetUserRequest{UserId: "unknown"},
			mockSetup: func(m *MockUserService) {
				m.On("GetUser", mock.Anything, "unknown").Return(nil, domain.ErrUserNotFound)
			},
			expectedCode: codes.NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockUserService)
			tt.mockSetup(mockService)
			handler := NewHandler(mockService)

			resp, err := handler.GetUser(context.Background(), tt.request)

			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
				if tt.checkResp != nil {
					tt.checkResp(t, resp)
				}
			} else {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, st.Code())
				if tt.expectedMsg != "" {
					assert.Contains(t, st.Message(), tt.expectedMsg)
				}
			}
			mockService.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты mapError
// =====================================

func TestMapError(t *testing.T) {
	handler := NewHandler(nil)

	tests := []struct {
		name         string
		domainError  error
		expectedCode codes.Code
	}{
		{"ErrUserNotFound", domain.ErrUserNotFound, codes.NotFound},
		{"ErrEmailExists", domain.ErrEmailExists, codes.AlreadyExists},
		{"ErrInvalidCredentials", domain.ErrInvalidCredentials, codes.Unauthenticated},
		{"ErrInvalidToken", domain.ErrInvalidToken, codes.Unauthenticated},
		{"ErrTokenRevoked", domain.ErrTokenRevoked, codes.Unauthenticated},
		{"ErrWeakPassword", domain.ErrWeakPassword, codes.InvalidArgument},
		{"ErrInvalidEmail", domain.ErrInvalidEmail, codes.InvalidArgument},
		{"ErrEmptyName", domain.ErrEmptyName, codes.InvalidArgument},
		{"unknown error", errors.New("db error"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := handler.mapError(context.Background(), tt.domainError, "Test")
			st, ok := status.FromError(grpcErr)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

// =====================================
// Тесты domainToProto и NewHandler
// =====================================

func TestDomainToProto(t *testing.T) {
	handler := NewHandler(nil)
	now := time.Now()
	user := &domain.User{
		ID:        "user-123",
		Email:     "test@example.com",
		Name:      "Тест",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
	}

	protoUser := handler.domainToProto(user)

	assert.Equal(t, user.ID, protoUser.GetId())
	assert.Equal(t, user.Email, protoUser.GetEmail())
	assert.Equal(t, user.Name, protoUser.GetName())
	assert.Equal(t, now.Unix(), protoUser.GetCreatedAt())
	assert.Equal(t, now.Add(time.Hour).Unix(), protoUser.GetUpdatedAt())
}

func TestNewHandler(t *testing.T) {
	mockService := new(MockUserService)
	handler := NewHandler(mockService)

	assert.NotNil(t, handler)
	assert.Equal(t, mockService, handler.userService)
}
