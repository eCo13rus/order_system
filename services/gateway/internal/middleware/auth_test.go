package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/order-system/services/gateway/internal/client"
	"example.com/order-system/services/gateway/internal/httputil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockTokenValidator — мок для TokenValidator интерфейса.
type MockTokenValidator struct {
	ValidateTokenFunc func(ctx context.Context, token string) (*client.TokenInfo, error)
}

func (m *MockTokenValidator) ValidateToken(ctx context.Context, token string) (*client.TokenInfo, error) {
	if m.ValidateTokenFunc != nil {
		return m.ValidateTokenFunc(ctx, token)
	}
	return nil, errors.New("ValidateTokenFunc not set")
}

// TestAuthMiddleware проверяет все сценарии аутентификации.
func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		authHeader     string
		setupMock      func(*MockTokenValidator)
		expectedStatus int
		expectedError  string
		checkContext   func(*testing.T, *gin.Context)
	}{
		{
			name:       "Успешная аутентификация",
			authHeader: "Bearer valid-token-123",
			setupMock: func(m *MockTokenValidator) {
				m.ValidateTokenFunc = func(ctx context.Context, token string) (*client.TokenInfo, error) {
					if token != "valid-token-123" {
						return nil, errors.New("unexpected token")
					}
					return &client.TokenInfo{
						Valid:  true,
						UserID: "user-uuid-456",
						Email:  "test@example.com",
						JTI:    "jti-789",
					}, nil
				}
			},
			expectedStatus: http.StatusOK, // c.Next() вызван, статус по умолчанию
			checkContext: func(t *testing.T, c *gin.Context) {
				userID, exists := c.Get("user_id")
				assert.True(t, exists, "user_id должен быть в контексте")
				assert.Equal(t, "user-uuid-456", userID)

				email, exists := c.Get("email")
				assert.True(t, exists, "email должен быть в контексте")
				assert.Equal(t, "test@example.com", email)

				jti, exists := c.Get("jti")
				assert.True(t, exists, "jti должен быть в контексте")
				assert.Equal(t, "jti-789", jti)
			},
		},
		{
			name:           "Отсутствует токен",
			authHeader:     "",
			setupMock:      func(m *MockTokenValidator) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:           "Пустой Bearer токен",
			authHeader:     "Bearer ",
			setupMock:      func(m *MockTokenValidator) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:           "Неверный формат — без Bearer",
			authHeader:     "just-a-token",
			setupMock:      func(m *MockTokenValidator) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:       "Ошибка валидации — gRPC недоступен",
			authHeader: "Bearer some-token",
			setupMock: func(m *MockTokenValidator) {
				m.ValidateTokenFunc = func(ctx context.Context, token string) (*client.TokenInfo, error) {
					return nil, errors.New("connection refused")
				}
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:       "Токен невалиден (Valid=false)",
			authHeader: "Bearer expired-token",
			setupMock: func(m *MockTokenValidator) {
				m.ValidateTokenFunc = func(ctx context.Context, token string) (*client.TokenInfo, error) {
					return &client.TokenInfo{
						Valid:  false, // Токен истёк или в blacklist
						UserID: "",
						Email:  "",
						JTI:    "",
					}, nil
				}
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:       "Bearer регистронезависимый",
			authHeader: "bearer lowercase-token",
			setupMock: func(m *MockTokenValidator) {
				m.ValidateTokenFunc = func(ctx context.Context, token string) (*client.TokenInfo, error) {
					return &client.TokenInfo{
						Valid:  true,
						UserID: "user-123",
						Email:  "user@example.com",
						JTI:    "jti-abc",
					}, nil
				}
			},
			expectedStatus: http.StatusOK,
			checkContext: func(t *testing.T, c *gin.Context) {
				userID, _ := c.Get("user_id")
				assert.Equal(t, "user-123", userID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Подготавливаем мок
			mockValidator := &MockTokenValidator{}
			tt.setupMock(mockValidator)

			// Создаём middleware
			mw := NewAuthMiddleware(mockValidator)

			// Создаём тестовый контекст
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)

			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}

			// Вызываем middleware
			handler := mw.Handle()
			handler(c)

			// Проверяем статус
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Проверяем ошибку если ожидается
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}

			// Дополнительные проверки контекста
			if tt.checkContext != nil {
				tt.checkContext(t, c)
			}
		})
	}
}

// TestExtractBearerToken проверяет извлечение токена из Authorization header.
func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		expected      string
	}{
		{
			name:          "валидный Bearer токен",
			authorization: "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9",
			expected:      "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:          "Bearer с пробелами",
			authorization: "Bearer   token_with_spaces   ",
			expected:      "token_with_spaces",
		},
		{
			name:          "bearer в нижнем регистре",
			authorization: "bearer lowercase_token",
			expected:      "lowercase_token",
		},
		{
			name:          "BEARER в верхнем регистре",
			authorization: "BEARER uppercase_token",
			expected:      "uppercase_token",
		},
		{
			name:          "без Bearer префикса",
			authorization: "just_token",
			expected:      "",
		},
		{
			name:          "пустой заголовок",
			authorization: "",
			expected:      "",
		},
		{
			name:          "только Bearer без токена",
			authorization: "Bearer ",
			expected:      "",
		},
		{
			name:          "Basic auth (не Bearer)",
			authorization: "Basic dXNlcjpwYXNz",
			expected:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authorization != "" {
				c.Request.Header.Set("Authorization", tt.authorization)
			}

			result := httputil.ExtractBearerToken(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNewAuthMiddleware проверяет создание middleware.
func TestNewAuthMiddleware(t *testing.T) {
	mockValidator := &MockTokenValidator{}
	mw := NewAuthMiddleware(mockValidator)

	assert.NotNil(t, mw)
	assert.Equal(t, mockValidator, mw.tokenValidator)
}

// TestAuthMiddleware_Integration — интеграционный тест с реальным UserClient.
// Пропускается по умолчанию.
func TestAuthMiddleware_Integration(t *testing.T) {
	t.Skip("Интеграционный тест — требует запущенный User Service")

	userClient, err := client.NewUserClient(client.UserClientConfig{
		Addr: "localhost:50051",
	})
	require.NoError(t, err)
	defer func() { _ = userClient.Close() }()

	mw := NewAuthMiddleware(userClient)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	c.Request.Header.Set("Authorization", "Bearer invalid_token")

	handler := mw.Handle()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
