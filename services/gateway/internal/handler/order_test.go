// Package handler содержит unit тесты для OrderHandler.
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

// MockOrderService — мок для OrderService.
type MockOrderService struct {
	CreateOrderFunc func(ctx context.Context, userID, idempotencyKey string, items []client.OrderItem) (*client.CreateOrderResult, error)
	GetOrderFunc    func(ctx context.Context, orderID string) (*client.Order, error)
	ListOrdersFunc  func(ctx context.Context, userID string, statusFilter *string, page, pageSize int) (*client.ListOrdersResult, error)
	CancelOrderFunc func(ctx context.Context, orderID string) error
}

func (m *MockOrderService) CreateOrder(ctx context.Context, userID, idempotencyKey string, items []client.OrderItem) (*client.CreateOrderResult, error) {
	if m.CreateOrderFunc != nil {
		return m.CreateOrderFunc(ctx, userID, idempotencyKey, items)
	}
	return nil, nil
}

func (m *MockOrderService) GetOrder(ctx context.Context, orderID string) (*client.Order, error) {
	if m.GetOrderFunc != nil {
		return m.GetOrderFunc(ctx, orderID)
	}
	return nil, nil
}

func (m *MockOrderService) ListOrders(ctx context.Context, userID string, st *string, page, pageSize int) (*client.ListOrdersResult, error) {
	if m.ListOrdersFunc != nil {
		return m.ListOrdersFunc(ctx, userID, st, page, pageSize)
	}
	return nil, nil
}

func (m *MockOrderService) CancelOrder(ctx context.Context, orderID string) error {
	if m.CancelOrderFunc != nil {
		return m.CancelOrderFunc(ctx, orderID)
	}
	return nil
}

// setupTestRouter создаёт Gin router для тестов с установленным user_id.
func setupTestRouter(handler *OrderHandler, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Middleware для установки user_id в контекст (имитация JWT middleware)
	r.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("user_id", userID)
		}
		c.Next()
	})

	// Регистрация маршрутов
	r.POST("/api/v1/orders", handler.CreateOrder)
	r.GET("/api/v1/orders", handler.ListOrders)
	r.GET("/api/v1/orders/:id", handler.GetOrder)
	r.DELETE("/api/v1/orders/:id", handler.CancelOrder)

	return r
}

// validCreateOrderRequest возвращает валидный запрос на создание заказа.
func validCreateOrderRequest() CreateOrderRequest {
	return CreateOrderRequest{
		IdempotencyKey: "test-key-123",
		Items: []CreateOrderItemRequest{
			{
				ProductID:   "550e8400-e29b-41d4-a716-446655440000",
				ProductName: "Тестовый товар",
				Quantity:    2,
				UnitPrice:   MoneyRequest{Amount: 1000, Currency: "RUB"},
			},
		},
	}
}

// validOrder возвращает валидный Order для тестов.
func validOrder(userID string) *client.Order {
	return &client.Order{
		ID:     "order-123",
		UserID: userID,
		Status: "PENDING",
		Items: []client.OrderItem{
			{
				ProductID:   "550e8400-e29b-41d4-a716-446655440000",
				ProductName: "Тестовый товар",
				Quantity:    2,
				UnitPrice:   client.Money{Amount: 1000, Currency: "RUB"},
			},
		},
		TotalAmount: client.Money{Amount: 2000, Currency: "RUB"},
		CreatedAt:   1735500000,
		UpdatedAt:   1735500000,
	}
}

// =====================================
// Тесты CreateOrder
// =====================================

func TestCreateOrder_Success(t *testing.T) {
	mock := &MockOrderService{
		CreateOrderFunc: func(_ context.Context, userID, idempotencyKey string, items []client.OrderItem) (*client.CreateOrderResult, error) {
			assert.Equal(t, "user-123", userID)
			assert.Equal(t, "test-key-123", idempotencyKey)
			assert.Len(t, items, 1)
			return &client.CreateOrderResult{OrderID: "order-123", Status: "PENDING"}, nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	body, _ := json.Marshal(validCreateOrderRequest())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp CreateOrderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "order-123", resp.OrderID)
	assert.Equal(t, "PENDING", resp.Status)
}

func TestCreateOrder_Unauthorized(t *testing.T) {
	handler := NewOrderHandler(&MockOrderService{})
	router := setupTestRouter(handler, "") // Пустой userID = нет авторизации

	body, _ := json.Marshal(validCreateOrderRequest())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateOrder_InvalidBody(t *testing.T) {
	handler := NewOrderHandler(&MockOrderService{})
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateOrder_GRPCError(t *testing.T) {
	mock := &MockOrderService{
		CreateOrderFunc: func(_ context.Context, _, _ string, _ []client.OrderItem) (*client.CreateOrderResult, error) {
			return nil, status.Error(codes.InvalidArgument, "некорректные данные")
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	body, _ := json.Marshal(validCreateOrderRequest())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =====================================
// Тесты GetOrder
// =====================================

func TestGetOrder_Success(t *testing.T) {
	userID := "user-123"
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, orderID string) (*client.Order, error) {
			assert.Equal(t, "order-123", orderID)
			return validOrder(userID), nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, userID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/order-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp GetOrderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "order-123", resp.Order.ID)
}

func TestGetOrder_NotFound(t *testing.T) {
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, _ string) (*client.Order, error) {
			return nil, status.Error(codes.NotFound, "заказ не найден")
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/non-existent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetOrder_Forbidden(t *testing.T) {
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, _ string) (*client.Order, error) {
			return validOrder("other-user"), nil // Заказ принадлежит другому пользователю
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/order-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// =====================================
// Тесты ListOrders
// =====================================

func TestListOrders_Success(t *testing.T) {
	userID := "user-123"
	mock := &MockOrderService{
		ListOrdersFunc: func(_ context.Context, uid string, st *string, page, pageSize int) (*client.ListOrdersResult, error) {
			assert.Equal(t, userID, uid)
			assert.Nil(t, st)
			assert.Equal(t, 1, page)
			assert.Equal(t, 20, pageSize)
			return &client.ListOrdersResult{
				Orders:     []client.Order{*validOrder(userID)},
				Total:      1,
				TotalPages: 1,
			}, nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, userID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ListOrdersResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Orders, 1)
	assert.Equal(t, int64(1), resp.Pagination.TotalItems)
}

func TestListOrders_WithPagination(t *testing.T) {
	mock := &MockOrderService{
		ListOrdersFunc: func(_ context.Context, _ string, _ *string, page, pageSize int) (*client.ListOrdersResult, error) {
			assert.Equal(t, 2, page)
			assert.Equal(t, 10, pageSize)
			return &client.ListOrdersResult{Orders: []client.Order{}, Total: 0, TotalPages: 0}, nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders?page=2&page_size=10", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListOrders_WithStatusFilter(t *testing.T) {
	mock := &MockOrderService{
		ListOrdersFunc: func(_ context.Context, _ string, st *string, _, _ int) (*client.ListOrdersResult, error) {
			require.NotNil(t, st)
			assert.Equal(t, "PENDING", *st)
			return &client.ListOrdersResult{Orders: []client.Order{}, Total: 0, TotalPages: 0}, nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders?status=PENDING", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListOrders_InvalidStatus(t *testing.T) {
	handler := NewOrderHandler(&MockOrderService{})
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders?status=INVALID", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =====================================
// Тесты CancelOrder
// =====================================

func TestCancelOrder_Success(t *testing.T) {
	userID := "user-123"
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, _ string) (*client.Order, error) {
			return validOrder(userID), nil
		},
		CancelOrderFunc: func(_ context.Context, orderID string) error {
			assert.Equal(t, "order-123", orderID)
			return nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, userID)

	// DELETE без body — reason удалён, failure_reason только для FAILED (Saga)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/orders/order-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp CancelOrderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
}

func TestCancelOrder_NotFound(t *testing.T) {
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, _ string) (*client.Order, error) {
			return nil, status.Error(codes.NotFound, "заказ не найден")
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/orders/non-existent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCancelOrder_Forbidden(t *testing.T) {
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, _ string) (*client.Order, error) {
			return validOrder("other-user"), nil
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, "user-123")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/orders/order-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCancelOrder_FailedPrecondition(t *testing.T) {
	userID := "user-123"
	mock := &MockOrderService{
		GetOrderFunc: func(_ context.Context, _ string) (*client.Order, error) {
			return validOrder(userID), nil
		},
		CancelOrderFunc: func(_ context.Context, _ string) error {
			return status.Error(codes.FailedPrecondition, "заказ нельзя отменить")
		},
	}

	handler := NewOrderHandler(mock)
	router := setupTestRouter(handler, userID)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/orders/order-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}
