package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestHandleGRPCError_AllCodes проверяет маппинг всех gRPC кодов в HTTP статусы.
func TestHandleGRPCError_AllCodes(t *testing.T) {
	tests := []struct {
		name           string
		grpcCode       codes.Code
		grpcMessage    string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "InvalidArgument → 400 Bad Request",
			grpcCode:       codes.InvalidArgument,
			grpcMessage:    "email обязателен",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_argument",
		},
		{
			name:           "NotFound → 404 Not Found",
			grpcCode:       codes.NotFound,
			grpcMessage:    "пользователь не найден",
			expectedStatus: http.StatusNotFound,
			expectedError:  "not_found",
		},
		{
			name:           "AlreadyExists → 409 Conflict",
			grpcCode:       codes.AlreadyExists,
			grpcMessage:    "пользователь уже существует",
			expectedStatus: http.StatusConflict,
			expectedError:  "already_exists",
		},
		{
			name:           "Unauthenticated → 401 Unauthorized",
			grpcCode:       codes.Unauthenticated,
			grpcMessage:    "неверный токен",
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthenticated",
		},
		{
			name:           "PermissionDenied → 403 Forbidden",
			grpcCode:       codes.PermissionDenied,
			grpcMessage:    "доступ запрещён",
			expectedStatus: http.StatusForbidden,
			expectedError:  "permission_denied",
		},
		{
			name:           "Unavailable → 503 Service Unavailable",
			grpcCode:       codes.Unavailable,
			grpcMessage:    "сервис временно недоступен",
			expectedStatus: http.StatusServiceUnavailable,
			expectedError:  "service_unavailable",
		},
		{
			name:           "Unknown → 500 Internal Server Error",
			grpcCode:       codes.Unknown,
			grpcMessage:    "неизвестная ошибка",
			expectedStatus: http.StatusInternalServerError,
			expectedError:  "internal_error",
		},
		{
			name:           "Internal → 500 Internal Server Error",
			grpcCode:       codes.Internal,
			grpcMessage:    "внутренняя ошибка gRPC",
			expectedStatus: http.StatusInternalServerError,
			expectedError:  "internal_error",
		},
		{
			name:           "DeadlineExceeded → 500 (default)",
			grpcCode:       codes.DeadlineExceeded,
			grpcMessage:    "превышен таймаут",
			expectedStatus: http.StatusInternalServerError,
			expectedError:  "internal_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			// Создаём gRPC ошибку с указанным кодом
			grpcErr := status.Error(tt.grpcCode, tt.grpcMessage)

			// Вызываем тестируемую функцию
			HandleGRPCError(c, grpcErr, "TestMethod")

			// Проверяем HTTP статус
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Проверяем тело ответа
			var resp ErrorResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err, "ответ должен быть валидным JSON")

			assert.Equal(t, tt.expectedError, resp.Error)
			assert.Equal(t, tt.grpcMessage, resp.Message)
		})
	}
}

// TestHandleGRPCError_NonGRPCError проверяет обработку не-gRPC ошибок.
func TestHandleGRPCError_NonGRPCError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	// Обычная Go ошибка (не gRPC)
	plainErr := errors.New("connection refused")

	HandleGRPCError(c, plainErr, "TestMethod")

	// Должен вернуть 500 Internal Server Error
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "internal_error", resp.Error)
	assert.Equal(t, "Внутренняя ошибка сервера", resp.Message)
}

// TestHandleGRPCError_NilError проверяет обработку nil ошибки.
// nil — это баг в вызывающем коде, функция должна вернуть 500 и залогировать ошибку.
func TestHandleGRPCError_NilError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	// nil ошибка — баг в коде, должен вернуть 500 Internal Server Error
	HandleGRPCError(c, nil, "TestMethod")

	// Проверяем, что guard сработал корректно
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "internal_error", resp.Error)
	assert.Equal(t, "Внутренняя ошибка сервера", resp.Message)
}

// TestErrorResponse_JSONFormat проверяет формат JSON ответа.
func TestErrorResponse_JSONFormat(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	grpcErr := status.Error(codes.NotFound, "пользователь не найден")
	HandleGRPCError(c, grpcErr, "GetUser")

	// Проверяем структуру JSON
	var rawResp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &rawResp)
	require.NoError(t, err)

	// Должны быть только поля "error" и "message"
	assert.Len(t, rawResp, 2, "ответ должен содержать ровно 2 поля")
	assert.Contains(t, rawResp, "error")
	assert.Contains(t, rawResp, "message")

	// Content-Type должен быть application/json
	contentType := w.Header().Get("Content-Type")
	assert.Contains(t, contentType, "application/json")
}
