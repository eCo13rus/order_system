package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"example.com/order-system/services/gateway/internal/client"
)

// MockUserService — мок для UserService с функциональными полями.
// Позволяет гибко настраивать поведение для каждого теста.
type MockUserService struct {
	RegisterFunc      func(ctx context.Context, email, password, name string) (string, error)
	LoginFunc         func(ctx context.Context, email, password string) (*client.LoginResult, error)
	LogoutFunc        func(ctx context.Context, accessToken string) error
	GetUserFunc       func(ctx context.Context, userID string) (*client.User, error)
	ValidateTokenFunc func(ctx context.Context, accessToken string) (*client.TokenInfo, error)
}

func (m *MockUserService) Register(ctx context.Context, email, password, name string) (string, error) {
	if m.RegisterFunc != nil {
		return m.RegisterFunc(ctx, email, password, name)
	}
	return "", nil
}

func (m *MockUserService) Login(ctx context.Context, email, password string) (*client.LoginResult, error) {
	if m.LoginFunc != nil {
		return m.LoginFunc(ctx, email, password)
	}
	return nil, nil
}

func (m *MockUserService) Logout(ctx context.Context, accessToken string) error {
	if m.LogoutFunc != nil {
		return m.LogoutFunc(ctx, accessToken)
	}
	return nil
}

func (m *MockUserService) GetUser(ctx context.Context, userID string) (*client.User, error) {
	if m.GetUserFunc != nil {
		return m.GetUserFunc(ctx, userID)
	}
	return nil, nil
}

func (m *MockUserService) ValidateToken(ctx context.Context, accessToken string) (*client.TokenInfo, error) {
	if m.ValidateTokenFunc != nil {
		return m.ValidateTokenFunc(ctx, accessToken)
	}
	return nil, nil
}

// TestAuthHandler_Register проверяет обработчик регистрации.
func TestAuthHandler_Register(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    interface{}
		setupMock      func(*MockUserService)
		expectedStatus int
		expectedError  string
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "Успешная регистрация",
			requestBody: RegisterRequest{
				Email:    "test@example.com",
				Password: "password123",
				Name:     "Test User",
			},
			setupMock: func(m *MockUserService) {
				m.RegisterFunc = func(ctx context.Context, email, password, name string) (string, error) {
					return "user-uuid-123", nil
				}
			},
			expectedStatus: http.StatusCreated,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp RegisterResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "user-uuid-123", resp.UserID)
			},
		},
		{
			name:           "Невалидный JSON",
			requestBody:    "invalid json",
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "Ошибка валидации — отсутствует email",
			requestBody: map[string]string{
				"password": "password123",
				"name":     "Test User",
			},
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "Ошибка валидации — короткий пароль",
			requestBody: map[string]string{
				"email":    "test@example.com",
				"password": "short",
				"name":     "Test User",
			},
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "Ошибка валидации — невалидный email",
			requestBody: map[string]string{
				"email":    "not-an-email",
				"password": "password123",
				"name":     "Test User",
			},
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "gRPC ошибка — пользователь уже существует",
			requestBody: RegisterRequest{
				Email:    "existing@example.com",
				Password: "password123",
				Name:     "Existing User",
			},
			setupMock: func(m *MockUserService) {
				m.RegisterFunc = func(ctx context.Context, email, password, name string) (string, error) {
					return "", status.Error(codes.AlreadyExists, "пользователь с таким email уже существует")
				}
			},
			expectedStatus: http.StatusConflict,
			expectedError:  "already_exists",
		},
		{
			name: "gRPC ошибка — сервис недоступен",
			requestBody: RegisterRequest{
				Email:    "test@example.com",
				Password: "password123",
				Name:     "Test User",
			},
			setupMock: func(m *MockUserService) {
				m.RegisterFunc = func(ctx context.Context, email, password, name string) (string, error) {
					return "", status.Error(codes.Unavailable, "сервис временно недоступен")
				}
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedError:  "service_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Подготавливаем мок
			mockService := &MockUserService{}
			tt.setupMock(mockService)

			// Создаём handler
			handler := NewAuthHandler(mockService)

			// Подготавливаем запрос
			var body []byte
			switch v := tt.requestBody.(type) {
			case string:
				body = []byte(v)
			default:
				var err error
				body, err = json.Marshal(v)
				require.NoError(t, err)
			}

			// Создаём тестовый контекст Gin
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			// Вызываем handler
			handler.Register(c)

			// Проверяем статус
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Проверяем ошибку если ожидается
			if tt.expectedError != "" {
				var resp map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedError, resp["error"])
			}

			// Дополнительные проверки
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

// TestAuthHandler_Login проверяет обработчик входа.
func TestAuthHandler_Login(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    interface{}
		setupMock      func(*MockUserService)
		expectedStatus int
		expectedError  string
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "Успешный вход",
			requestBody: LoginRequest{
				Email:    "test@example.com",
				Password: "password123",
			},
			setupMock: func(m *MockUserService) {
				m.LoginFunc = func(ctx context.Context, email, password string) (*client.LoginResult, error) {
					return &client.LoginResult{
						AccessToken:  "access-token-123",
						RefreshToken: "refresh-token-456",
						ExpiresAt:    1735500000,
					}, nil
				}
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp LoginResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "access-token-123", resp.AccessToken)
				assert.Equal(t, "refresh-token-456", resp.RefreshToken)
				assert.Equal(t, int64(1735500000), resp.ExpiresAt)
			},
		},
		{
			name:           "Невалидный JSON",
			requestBody:    "{broken json",
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "Ошибка валидации — отсутствует password",
			requestBody: map[string]string{
				"email": "test@example.com",
			},
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "Неверный пароль",
			requestBody: LoginRequest{
				Email:    "test@example.com",
				Password: "wrong-password",
			},
			setupMock: func(m *MockUserService) {
				m.LoginFunc = func(ctx context.Context, email, password string) (*client.LoginResult, error) {
					return nil, status.Error(codes.Unauthenticated, "неверный email или пароль")
				}
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthenticated",
		},
		{
			name: "Пользователь не найден",
			requestBody: LoginRequest{
				Email:    "notfound@example.com",
				Password: "password123",
			},
			setupMock: func(m *MockUserService) {
				m.LoginFunc = func(ctx context.Context, email, password string) (*client.LoginResult, error) {
					return nil, status.Error(codes.NotFound, "пользователь не найден")
				}
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "not_found",
		},
		{
			name: "Внутренняя ошибка gRPC",
			requestBody: LoginRequest{
				Email:    "test@example.com",
				Password: "password123",
			},
			setupMock: func(m *MockUserService) {
				m.LoginFunc = func(ctx context.Context, email, password string) (*client.LoginResult, error) {
					return nil, status.Error(codes.Internal, "внутренняя ошибка")
				}
			},
			expectedStatus: http.StatusInternalServerError,
			expectedError:  "internal_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Подготавливаем мок
			mockService := &MockUserService{}
			tt.setupMock(mockService)

			// Создаём handler
			handler := NewAuthHandler(mockService)

			// Подготавливаем запрос
			var body []byte
			switch v := tt.requestBody.(type) {
			case string:
				body = []byte(v)
			default:
				var err error
				body, err = json.Marshal(v)
				require.NoError(t, err)
			}

			// Создаём тестовый контекст Gin
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			// Вызываем handler
			handler.Login(c)

			// Проверяем статус
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Проверяем ошибку если ожидается
			if tt.expectedError != "" {
				var resp map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedError, resp["error"])
			}

			// Дополнительные проверки
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

// TestAuthHandler_Logout проверяет обработчик выхода.
func TestAuthHandler_Logout(t *testing.T) {
	tests := []struct {
		name           string
		authHeader     string
		setupMock      func(*MockUserService)
		expectedStatus int
		expectedError  string
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:       "Успешный выход",
			authHeader: "Bearer valid-access-token",
			setupMock: func(m *MockUserService) {
				m.LogoutFunc = func(ctx context.Context, accessToken string) error {
					// Проверяем, что токен извлечён корректно
					if accessToken != "valid-access-token" {
						return status.Error(codes.InvalidArgument, "неверный токен")
					}
					return nil
				}
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, true, resp["success"])
			},
		},
		{
			name:           "Отсутствует токен",
			authHeader:     "",
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:           "Неверный формат токена — без Bearer",
			authHeader:     "some-token-without-bearer",
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:           "Неверный формат токена — пустой после Bearer",
			authHeader:     "Bearer ",
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:       "gRPC ошибка — токен невалиден",
			authHeader: "Bearer invalid-token",
			setupMock: func(m *MockUserService) {
				m.LogoutFunc = func(ctx context.Context, accessToken string) error {
					return status.Error(codes.Unauthenticated, "токен недействителен")
				}
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthenticated",
		},
		{
			name:       "gRPC ошибка — сервис недоступен",
			authHeader: "Bearer valid-token",
			setupMock: func(m *MockUserService) {
				m.LogoutFunc = func(ctx context.Context, accessToken string) error {
					return status.Error(codes.Unavailable, "сервис временно недоступен")
				}
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedError:  "service_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Подготавливаем мок
			mockService := &MockUserService{}
			tt.setupMock(mockService)

			// Создаём handler
			handler := NewAuthHandler(mockService)

			// Создаём тестовый контекст Gin
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)

			// Устанавливаем Authorization header если указан
			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}

			// Вызываем handler
			handler.Logout(c)

			// Проверяем статус
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Проверяем ошибку если ожидается
			if tt.expectedError != "" {
				var resp map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedError, resp["error"])
			}

			// Дополнительные проверки
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}
