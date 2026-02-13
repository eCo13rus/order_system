package handler

import (
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

// TestUserHandler_GetMe проверяет обработчик получения текущего пользователя.
func TestUserHandler_GetMe(t *testing.T) {
	tests := []struct {
		name           string
		userIDInCtx    interface{} // nil если не устанавливать, string если установить
		setupMock      func(*MockUserService)
		expectedStatus int
		expectedError  string
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:        "Успешное получение пользователя",
			userIDInCtx: "user-uuid-123",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					if userID != "user-uuid-123" {
						return nil, status.Error(codes.NotFound, "пользователь не найден")
					}
					return &client.User{
						ID:        "user-uuid-123",
						Email:     "test@example.com",
						Name:      "Test User",
						CreatedAt: 1735400000,
						UpdatedAt: 1735450000,
					}, nil
				}
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp UserResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "user-uuid-123", resp.ID)
				assert.Equal(t, "test@example.com", resp.Email)
				assert.Equal(t, "Test User", resp.Name)
				assert.Equal(t, int64(1735400000), resp.CreatedAt)
				assert.Equal(t, int64(1735450000), resp.UpdatedAt)
			},
		},
		{
			name:           "Отсутствует user_id в контексте",
			userIDInCtx:    nil,
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:        "gRPC ошибка — пользователь не найден",
			userIDInCtx: "non-existent-user",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.NotFound, "пользователь не найден")
				}
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "not_found",
		},
		{
			name:        "gRPC ошибка — сервис недоступен",
			userIDInCtx: "user-uuid-123",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.Unavailable, "сервис временно недоступен")
				}
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedError:  "service_unavailable",
		},
		{
			name:        "gRPC ошибка — внутренняя ошибка",
			userIDInCtx: "user-uuid-123",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.Internal, "ошибка базы данных")
				}
			},
			expectedStatus: http.StatusInternalServerError,
			expectedError:  "internal_error",
		},
		{
			name:           "user_id неверного типа (не string)",
			userIDInCtx:    12345, // int вместо string — баг в middleware
			setupMock:      func(m *MockUserService) {},
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
			handler := NewUserHandler(mockService)

			// Создаём тестовый контекст Gin
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)

			// Устанавливаем user_id в контексте если указан
			if tt.userIDInCtx != nil {
				c.Set("user_id", tt.userIDInCtx)
			}

			// Вызываем handler
			handler.GetMe(c)

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

// TestUserHandler_GetUser проверяет обработчик получения пользователя по ID.
// Учитывает IDOR-защиту: user_id из JWT должен совпадать с запрашиваемым :id.
func TestUserHandler_GetUser(t *testing.T) {
	tests := []struct {
		name           string
		paramID        string      // параметр :id из URL
		userIDInCtx    interface{} // user_id из JWT; nil — не устанавливать
		setupMock      func(*MockUserService)
		expectedStatus int
		expectedError  string
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:        "Успешное получение своего профиля по ID",
			paramID:     "user-uuid-456",
			userIDInCtx: "user-uuid-456", // совпадает с paramID — разрешено
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					if userID != "user-uuid-456" {
						return nil, status.Error(codes.NotFound, "пользователь не найден")
					}
					return &client.User{
						ID:        "user-uuid-456",
						Email:     "other@example.com",
						Name:      "Other User",
						CreatedAt: 1735300000,
						UpdatedAt: 1735350000,
					}, nil
				}
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp UserResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "user-uuid-456", resp.ID)
				assert.Equal(t, "other@example.com", resp.Email)
				assert.Equal(t, "Other User", resp.Name)
			},
		},
		{
			name:           "Пустой ID пользователя",
			paramID:        "",
			userIDInCtx:    "some-user",
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name:           "IDOR — попытка доступа к чужому профилю",
			paramID:        "other-user-uuid",
			userIDInCtx:    "my-user-uuid", // не совпадает с paramID
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusForbidden,
			expectedError:  "forbidden",
		},
		{
			name:           "Отсутствует user_id в контексте (нет авторизации)",
			paramID:        "user-uuid-123",
			userIDInCtx:    nil,
			setupMock:      func(m *MockUserService) {},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized",
		},
		{
			name:        "Пользователь не найден",
			paramID:     "non-existent-uuid",
			userIDInCtx: "non-existent-uuid", // совпадает — IDOR пройден, но gRPC вернёт NotFound
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.NotFound, "пользователь не найден")
				}
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "not_found",
		},
		{
			name:        "gRPC ошибка — невалидный ID",
			paramID:     "invalid-format",
			userIDInCtx: "invalid-format",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.InvalidArgument, "невалидный формат ID")
				}
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_argument",
		},
		{
			name:        "gRPC ошибка — доступ запрещён",
			paramID:     "private-user-uuid",
			userIDInCtx: "private-user-uuid",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.PermissionDenied, "доступ запрещён")
				}
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "permission_denied",
		},
		{
			name:        "gRPC ошибка — сервис недоступен",
			paramID:     "user-uuid-123",
			userIDInCtx: "user-uuid-123",
			setupMock: func(m *MockUserService) {
				m.GetUserFunc = func(ctx context.Context, userID string) (*client.User, error) {
					return nil, status.Error(codes.Unavailable, "сервис временно недоступен")
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
			handler := NewUserHandler(mockService)

			// Создаём тестовый контекст Gin
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/users/"+tt.paramID, nil)

			// Устанавливаем параметр :id (Gin использует Params)
			c.Params = gin.Params{
				{Key: "id", Value: tt.paramID},
			}

			// Устанавливаем user_id в контексте (имитация JWT middleware)
			if tt.userIDInCtx != nil {
				c.Set("user_id", tt.userIDInCtx)
			}

			// Вызываем handler
			handler.GetUser(c)

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

// TestNewUserHandler проверяет создание UserHandler.
func TestNewUserHandler(t *testing.T) {
	mockService := &MockUserService{}
	handler := NewUserHandler(mockService)

	assert.NotNil(t, handler)
	assert.Equal(t, mockService, handler.userService)
}

// TestNewAuthHandler проверяет создание AuthHandler.
func TestNewAuthHandler(t *testing.T) {
	mockService := &MockUserService{}
	handler := NewAuthHandler(mockService)

	assert.NotNil(t, handler)
	assert.Equal(t, mockService, handler.userService)
}
