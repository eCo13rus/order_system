// Package grpc содержит unit тесты для gRPC Handler Order Service.
package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "example.com/order-system/proto/common/v1"
	orderv1 "example.com/order-system/proto/order/v1"
	"example.com/order-system/services/order/internal/domain"
	"example.com/order-system/services/order/internal/service"
)

// =====================================
// Мок для OrderService
// =====================================

// MockOrderService — мок для OrderService.
type MockOrderService struct {
	mock.Mock
}

var _ service.OrderService = (*MockOrderService)(nil)

func (m *MockOrderService) CreateOrder(ctx context.Context, userID, idempotencyKey string, items []domain.OrderItem) (*domain.Order, error) {
	args := m.Called(ctx, userID, idempotencyKey, items)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Order), args.Error(1)
}

func (m *MockOrderService) GetOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	args := m.Called(ctx, orderID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Order), args.Error(1)
}

func (m *MockOrderService) ListOrders(ctx context.Context, userID string, status *domain.OrderStatus, page, pageSize int) ([]*domain.Order, int64, error) {
	args := m.Called(ctx, userID, status, page, pageSize)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Order), args.Get(1).(int64), args.Error(2)
}

func (m *MockOrderService) CancelOrder(ctx context.Context, orderID string) error {
	return m.Called(ctx, orderID).Error(0)
}

// =====================================
// Тесты CreateOrder
// =====================================

// TestHandler_CreateOrder тестирует успешное создание заказа.
func TestHandler_CreateOrder(t *testing.T) {
	mockService := new(MockOrderService)

	createdOrder := &domain.Order{
		ID:     "order-123",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
		Items: []domain.OrderItem{
			{
				ProductID:   "product-1",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
			},
		},
		TotalAmount: domain.Money{Amount: 2000, Currency: "RUB"},
	}

	mockService.On("CreateOrder", mock.Anything, "user-123", "idem-key-123", mock.AnythingOfType("[]domain.OrderItem")).
		Return(createdOrder, nil)

	handler := NewHandler(mockService)

	req := &orderv1.CreateOrderRequest{
		UserId:         "user-123",
		IdempotencyKey: "idem-key-123",
		Items: []*orderv1.OrderItem{
			{
				ProductId:   "product-1",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   &commonv1.Money{Amount: 1000, Currency: "RUB"},
			},
		},
	}

	resp, err := handler.CreateOrder(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "order-123", resp.GetOrderId())
	assert.Equal(t, orderv1.OrderStatus_ORDER_STATUS_PENDING, resp.GetStatus())

	mockService.AssertExpectations(t)
}

// TestHandler_CreateOrder_EmptyUserID тестирует ошибку при пустом user_id.
func TestHandler_CreateOrder_EmptyUserID(t *testing.T) {
	mockService := new(MockOrderService)
	handler := NewHandler(mockService)

	req := &orderv1.CreateOrderRequest{
		UserId: "",
		Items: []*orderv1.OrderItem{
			{
				ProductId:   "product-1",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   &commonv1.Money{Amount: 1000, Currency: "RUB"},
			},
		},
	}

	resp, err := handler.CreateOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "user_id обязателен")

	// CreateOrder НЕ должен вызываться
	mockService.AssertNotCalled(t, "CreateOrder")
}

// TestHandler_CreateOrder_EmptyItems тестирует ошибку при пустом списке позиций.
func TestHandler_CreateOrder_EmptyItems(t *testing.T) {
	mockService := new(MockOrderService)
	handler := NewHandler(mockService)

	req := &orderv1.CreateOrderRequest{
		UserId: "user-123",
		Items:  []*orderv1.OrderItem{},
	}

	resp, err := handler.CreateOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "хотя бы одну позицию")

	// CreateOrder НЕ должен вызываться
	mockService.AssertNotCalled(t, "CreateOrder")
}

// TestHandler_CreateOrder_ValidationError тестирует ошибку валидации от сервиса.
func TestHandler_CreateOrder_ValidationError(t *testing.T) {
	tests := []struct {
		name         string
		serviceErr   error
		expectedCode codes.Code
	}{
		{
			name:         "ErrInvalidUserID",
			serviceErr:   domain.ErrInvalidUserID,
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "ErrEmptyOrderItems",
			serviceErr:   domain.ErrEmptyOrderItems,
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "ErrInvalidQuantity",
			serviceErr:   domain.ErrInvalidQuantity,
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "ErrInvalidPrice",
			serviceErr:   domain.ErrInvalidPrice,
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "ErrDuplicateOrder",
			serviceErr:   domain.ErrDuplicateOrder,
			expectedCode: codes.AlreadyExists,
		},
		{
			name:         "внутренняя ошибка",
			serviceErr:   errors.New("database error"),
			expectedCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockOrderService)
			mockService.On("CreateOrder", mock.Anything, "user-123", "", mock.Anything).
				Return(nil, tt.serviceErr)

			handler := NewHandler(mockService)

			req := &orderv1.CreateOrderRequest{
				UserId: "user-123",
				Items: []*orderv1.OrderItem{
					{
						ProductId:   "product-1",
						ProductName: "Товар 1",
						Quantity:    2,
						UnitPrice:   &commonv1.Money{Amount: 1000, Currency: "RUB"},
					},
				},
			}

			resp, err := handler.CreateOrder(context.Background(), req)

			require.Error(t, err)
			assert.Nil(t, resp)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())

			mockService.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты GetOrder
// =====================================

// TestHandler_GetOrder тестирует успешное получение заказа.
func TestHandler_GetOrder(t *testing.T) {
	mockService := new(MockOrderService)

	order := &domain.Order{
		ID:     "order-123",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
		Items: []domain.OrderItem{
			{
				ProductID:   "product-1",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
			},
		},
		TotalAmount: domain.Money{Amount: 2000, Currency: "RUB"},
	}

	mockService.On("GetOrder", mock.Anything, "order-123").Return(order, nil)

	handler := NewHandler(mockService)

	req := &orderv1.GetOrderRequest{
		OrderId: "order-123",
	}

	resp, err := handler.GetOrder(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetOrder())
	assert.Equal(t, "order-123", resp.GetOrder().GetId())
	assert.Equal(t, "user-123", resp.GetOrder().GetUserId())
	assert.Equal(t, orderv1.OrderStatus_ORDER_STATUS_PENDING, resp.GetOrder().GetStatus())
	assert.Len(t, resp.GetOrder().GetItems(), 1)
	assert.Equal(t, int64(2000), resp.GetOrder().GetTotalAmount().GetAmount())
	assert.Equal(t, "RUB", resp.GetOrder().GetTotalAmount().GetCurrency())

	mockService.AssertExpectations(t)
}

// TestHandler_GetOrder_EmptyOrderID тестирует ошибку при пустом order_id.
func TestHandler_GetOrder_EmptyOrderID(t *testing.T) {
	mockService := new(MockOrderService)
	handler := NewHandler(mockService)

	req := &orderv1.GetOrderRequest{
		OrderId: "",
	}

	resp, err := handler.GetOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "order_id обязателен")

	// GetOrder НЕ должен вызываться
	mockService.AssertNotCalled(t, "GetOrder")
}

// TestHandler_GetOrder_NotFound тестирует случай, когда заказ не найден.
func TestHandler_GetOrder_NotFound(t *testing.T) {
	mockService := new(MockOrderService)
	mockService.On("GetOrder", mock.Anything, "non-existent-order").
		Return(nil, domain.ErrOrderNotFound)

	handler := NewHandler(mockService)

	req := &orderv1.GetOrderRequest{
		OrderId: "non-existent-order",
	}

	resp, err := handler.GetOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	mockService.AssertExpectations(t)
}

// =====================================
// Тесты ListOrders
// =====================================

// TestHandler_ListOrders тестирует получение списка заказов с пагинацией.
func TestHandler_ListOrders(t *testing.T) {
	mockService := new(MockOrderService)

	orders := []*domain.Order{
		{
			ID:          "order-1",
			UserID:      "user-123",
			Status:      domain.OrderStatusPending,
			TotalAmount: domain.Money{Amount: 1000, Currency: "RUB"},
			Items:       []domain.OrderItem{},
		},
		{
			ID:          "order-2",
			UserID:      "user-123",
			Status:      domain.OrderStatusConfirmed,
			TotalAmount: domain.Money{Amount: 2000, Currency: "RUB"},
			Items:       []domain.OrderItem{},
		},
	}

	mockService.On("ListOrders", mock.Anything, "user-123", (*domain.OrderStatus)(nil), 2, 10).
		Return(orders, int64(15), nil)

	handler := NewHandler(mockService)

	req := &orderv1.ListOrdersRequest{
		UserId: "user-123",
		Pagination: &commonv1.Pagination{
			Page:     2,
			PageSize: 10,
		},
	}

	resp, err := handler.ListOrders(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.GetOrders(), 2)
	assert.Equal(t, int64(15), resp.GetPaginationMeta().GetTotalItems())
	assert.Equal(t, int32(2), resp.GetPaginationMeta().GetCurrentPage())
	assert.Equal(t, int32(10), resp.GetPaginationMeta().GetPageSize())
	assert.Equal(t, int32(2), resp.GetPaginationMeta().GetTotalPages()) // 15/10 = 1.5 -> 2

	mockService.AssertExpectations(t)
}

// TestHandler_ListOrders_WithStatusFilter тестирует фильтрацию по статусу.
func TestHandler_ListOrders_WithStatusFilter(t *testing.T) {
	mockService := new(MockOrderService)

	pendingStatus := domain.OrderStatusPending
	orders := []*domain.Order{
		{
			ID:          "order-1",
			UserID:      "user-123",
			Status:      domain.OrderStatusPending,
			TotalAmount: domain.Money{Amount: 1000, Currency: "RUB"},
			Items:       []domain.OrderItem{},
		},
	}

	mockService.On("ListOrders", mock.Anything, "user-123", &pendingStatus, 1, 20).
		Return(orders, int64(1), nil)

	handler := NewHandler(mockService)

	statusFilter := orderv1.OrderStatus_ORDER_STATUS_PENDING
	req := &orderv1.ListOrdersRequest{
		UserId:       "user-123",
		StatusFilter: &statusFilter,
	}

	resp, err := handler.ListOrders(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.GetOrders(), 1)

	mockService.AssertExpectations(t)
}

// TestHandler_ListOrders_EmptyUserID тестирует ошибку при пустом user_id.
func TestHandler_ListOrders_EmptyUserID(t *testing.T) {
	mockService := new(MockOrderService)
	handler := NewHandler(mockService)

	req := &orderv1.ListOrdersRequest{
		UserId: "",
	}

	resp, err := handler.ListOrders(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "user_id обязателен")

	// ListOrders НЕ должен вызываться
	mockService.AssertNotCalled(t, "ListOrders")
}

// TestHandler_ListOrders_DefaultPagination тестирует значения пагинации по умолчанию.
func TestHandler_ListOrders_DefaultPagination(t *testing.T) {
	mockService := new(MockOrderService)

	// По умолчанию page=1, pageSize=20
	mockService.On("ListOrders", mock.Anything, "user-123", (*domain.OrderStatus)(nil), 1, 20).
		Return([]*domain.Order{}, int64(0), nil)

	handler := NewHandler(mockService)

	req := &orderv1.ListOrdersRequest{
		UserId: "user-123",
		// Pagination не указан
	}

	resp, err := handler.ListOrders(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(1), resp.GetPaginationMeta().GetCurrentPage())
	assert.Equal(t, int32(20), resp.GetPaginationMeta().GetPageSize())

	mockService.AssertExpectations(t)
}

// =====================================
// Тесты CancelOrder
// =====================================

// TestHandler_CancelOrder тестирует успешную отмену заказа.
func TestHandler_CancelOrder(t *testing.T) {
	mockService := new(MockOrderService)
	mockService.On("CancelOrder", mock.Anything, "order-123").Return(nil)

	handler := NewHandler(mockService)

	req := &orderv1.CancelOrderRequest{
		OrderId: "order-123",
	}

	resp, err := handler.CancelOrder(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, "заказ успешно отменён", resp.GetMessage())

	mockService.AssertExpectations(t)
}

// TestHandler_CancelOrder_EmptyOrderID тестирует ошибку при пустом order_id.
func TestHandler_CancelOrder_EmptyOrderID(t *testing.T) {
	mockService := new(MockOrderService)
	handler := NewHandler(mockService)

	req := &orderv1.CancelOrderRequest{
		OrderId: "",
	}

	resp, err := handler.CancelOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "order_id обязателен")

	// CancelOrder НЕ должен вызываться
	mockService.AssertNotCalled(t, "CancelOrder")
}

// TestHandler_CancelOrder_NotFound тестирует отмену несуществующего заказа.
func TestHandler_CancelOrder_NotFound(t *testing.T) {
	mockService := new(MockOrderService)
	mockService.On("CancelOrder", mock.Anything, "non-existent-order").
		Return(domain.ErrOrderNotFound)

	handler := NewHandler(mockService)

	req := &orderv1.CancelOrderRequest{
		OrderId: "non-existent-order",
	}

	resp, err := handler.CancelOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	mockService.AssertExpectations(t)
}

// TestHandler_CancelOrder_WrongStatus тестирует ошибку при неподходящем статусе заказа.
func TestHandler_CancelOrder_WrongStatus(t *testing.T) {
	mockService := new(MockOrderService)
	mockService.On("CancelOrder", mock.Anything, "order-123").
		Return(domain.ErrOrderCannotCancel)

	handler := NewHandler(mockService)

	req := &orderv1.CancelOrderRequest{
		OrderId: "order-123",
	}

	resp, err := handler.CancelOrder(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	mockService.AssertExpectations(t)
}

// =====================================
// Тесты mapError
// =====================================

// TestMapError тестирует преобразование доменных ошибок в gRPC статусы.
func TestMapError(t *testing.T) {
	handler := NewHandler(nil)

	tests := []struct {
		name         string
		domainError  error
		expectedCode codes.Code
	}{
		{"ErrOrderNotFound", domain.ErrOrderNotFound, codes.NotFound},
		{"ErrDuplicateOrder", domain.ErrDuplicateOrder, codes.AlreadyExists},
		{"ErrOrderCannotCancel", domain.ErrOrderCannotCancel, codes.FailedPrecondition},
		{"ErrInvalidUserID", domain.ErrInvalidUserID, codes.InvalidArgument},
		{"ErrEmptyOrderItems", domain.ErrEmptyOrderItems, codes.InvalidArgument},
		{"ErrInvalidQuantity", domain.ErrInvalidQuantity, codes.InvalidArgument},
		{"ErrInvalidProductID", domain.ErrInvalidProductID, codes.InvalidArgument},
		{"ErrInvalidProductName", domain.ErrInvalidProductName, codes.InvalidArgument},
		{"ErrInvalidPrice", domain.ErrInvalidPrice, codes.InvalidArgument},
		{"unknown error", errors.New("database error"), codes.Internal},
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
