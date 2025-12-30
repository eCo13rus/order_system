// Package service содержит unit тесты для OrderService.
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"example.com/order-system/services/order/internal/domain"
)

// =====================================
// Мок для OrderRepository
// =====================================

// MockOrderRepository — мок для OrderRepository.
type MockOrderRepository struct {
	mock.Mock
}

func (m *MockOrderRepository) Create(ctx context.Context, order *domain.Order) error {
	return m.Called(ctx, order).Error(0)
}

func (m *MockOrderRepository) GetByID(ctx context.Context, orderID string) (*domain.Order, error) {
	args := m.Called(ctx, orderID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Order), args.Error(1)
}

func (m *MockOrderRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.Order, error) {
	args := m.Called(ctx, idempotencyKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Order), args.Error(1)
}

func (m *MockOrderRepository) ListByUserID(ctx context.Context, userID string, status *domain.OrderStatus, offset, limit int) ([]*domain.Order, int64, error) {
	args := m.Called(ctx, userID, status, offset, limit)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Order), args.Get(1).(int64), args.Error(2)
}

func (m *MockOrderRepository) UpdateStatus(ctx context.Context, orderID string, status domain.OrderStatus, paymentID, failureReason *string) error {
	return m.Called(ctx, orderID, status, paymentID, failureReason).Error(0)
}

// =====================================
// Тесты CreateOrder
// =====================================

// TestOrderService_CreateOrder тестирует успешное создание заказа.
func TestOrderService_CreateOrder(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	// Идемпотентность: заказ не найден (первый запрос)
	mockRepo.On("GetByIdempotencyKey", mock.Anything, "idem-key-123").
		Return(nil, domain.ErrOrderNotFound)
	mockRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Order")).
		Return(nil)

	svc := NewOrderService(mockRepo)

	items := []domain.OrderItem{
		{
			ProductID:   "product-1",
			ProductName: "Товар 1",
			Quantity:    2,
			UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
		},
	}

	order, err := svc.CreateOrder(context.Background(), "user-123", "idem-key-123", items)

	require.NoError(t, err)
	require.NotNil(t, order)
	assert.NotEmpty(t, order.ID)
	assert.Equal(t, "user-123", order.UserID)
	assert.Equal(t, domain.OrderStatusPending, order.Status)
	assert.Equal(t, int64(2000), order.TotalAmount.Amount) // 2 * 1000
	assert.Equal(t, "RUB", order.TotalAmount.Currency)
	assert.Len(t, order.Items, 1)

	mockRepo.AssertExpectations(t)
}

// TestOrderService_CreateOrder_Idempotency тестирует идемпотентность: повторный запрос с тем же ключом.
func TestOrderService_CreateOrder_Idempotency(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	existingOrder := &domain.Order{
		ID:             "existing-order-123",
		UserID:         "user-123",
		Status:         domain.OrderStatusPending,
		IdempotencyKey: "idem-key-123",
	}

	// Идемпотентность: заказ уже существует
	mockRepo.On("GetByIdempotencyKey", mock.Anything, "idem-key-123").
		Return(existingOrder, nil)

	svc := NewOrderService(mockRepo)

	items := []domain.OrderItem{
		{
			ProductID:   "product-1",
			ProductName: "Товар 1",
			Quantity:    2,
			UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
		},
	}

	order, err := svc.CreateOrder(context.Background(), "user-123", "idem-key-123", items)

	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, "existing-order-123", order.ID) // Вернулся существующий заказ

	// Create НЕ должен вызываться
	mockRepo.AssertNotCalled(t, "Create")
	mockRepo.AssertExpectations(t)
}

// TestOrderService_CreateOrder_ValidationError тестирует ошибки валидации.
func TestOrderService_CreateOrder_ValidationError(t *testing.T) {
	tests := []struct {
		name        string
		userID      string
		items       []domain.OrderItem
		expectedErr error
	}{
		{
			name:   "пустой UserID",
			userID: "",
			items: []domain.OrderItem{
				{
					ProductID:   "product-1",
					ProductName: "Товар 1",
					Quantity:    2,
					UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
				},
			},
			expectedErr: domain.ErrInvalidUserID,
		},
		{
			name:        "пустой список позиций",
			userID:      "user-123",
			items:       []domain.OrderItem{},
			expectedErr: domain.ErrEmptyOrderItems,
		},
		{
			name:   "невалидная позиция - пустой ProductID",
			userID: "user-123",
			items: []domain.OrderItem{
				{
					ProductID:   "",
					ProductName: "Товар 1",
					Quantity:    2,
					UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
				},
			},
			expectedErr: domain.ErrInvalidProductID,
		},
		{
			name:   "невалидная позиция - нулевое количество",
			userID: "user-123",
			items: []domain.OrderItem{
				{
					ProductID:   "product-1",
					ProductName: "Товар 1",
					Quantity:    0,
					UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
				},
			},
			expectedErr: domain.ErrInvalidQuantity,
		},
		{
			name:   "невалидная позиция - нулевая цена",
			userID: "user-123",
			items: []domain.OrderItem{
				{
					ProductID:   "product-1",
					ProductName: "Товар 1",
					Quantity:    2,
					UnitPrice:   domain.Money{Amount: 0, Currency: "RUB"},
				},
			},
			expectedErr: domain.ErrInvalidPrice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockOrderRepository)

			// GetByIdempotencyKey не вызывается при пустом ключе,
			// но может вызываться при непустом
			mockRepo.On("GetByIdempotencyKey", mock.Anything, mock.Anything).
				Return(nil, domain.ErrOrderNotFound).Maybe()

			svc := NewOrderService(mockRepo)

			order, err := svc.CreateOrder(context.Background(), tt.userID, "", tt.items)

			require.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr)
			assert.Nil(t, order)
		})
	}
}

// TestOrderService_CreateOrder_DBError тестирует ошибку БД при создании заказа.
func TestOrderService_CreateOrder_DBError(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	mockRepo.On("GetByIdempotencyKey", mock.Anything, "idem-key-123").
		Return(nil, domain.ErrOrderNotFound)
	mockRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Order")).
		Return(errors.New("database connection lost"))

	svc := NewOrderService(mockRepo)

	items := []domain.OrderItem{
		{
			ProductID:   "product-1",
			ProductName: "Товар 1",
			Quantity:    2,
			UnitPrice:   domain.Money{Amount: 1000, Currency: "RUB"},
		},
	}

	order, err := svc.CreateOrder(context.Background(), "user-123", "idem-key-123", items)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database connection lost")
	assert.Nil(t, order)

	mockRepo.AssertExpectations(t)
}

// =====================================
// Тесты GetOrder
// =====================================

// TestOrderService_GetOrder тестирует успешное получение заказа.
func TestOrderService_GetOrder(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	expectedOrder := &domain.Order{
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

	mockRepo.On("GetByID", mock.Anything, "order-123").Return(expectedOrder, nil)

	svc := NewOrderService(mockRepo)

	order, err := svc.GetOrder(context.Background(), "order-123")

	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, expectedOrder.ID, order.ID)
	assert.Equal(t, expectedOrder.UserID, order.UserID)
	assert.Equal(t, expectedOrder.Status, order.Status)

	mockRepo.AssertExpectations(t)
}

// TestOrderService_GetOrder_NotFound тестирует случай, когда заказ не найден.
func TestOrderService_GetOrder_NotFound(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	mockRepo.On("GetByID", mock.Anything, "non-existent-order").
		Return(nil, domain.ErrOrderNotFound)

	svc := NewOrderService(mockRepo)

	order, err := svc.GetOrder(context.Background(), "non-existent-order")

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrOrderNotFound)
	assert.Nil(t, order)

	mockRepo.AssertExpectations(t)
}

// TestOrderService_GetOrder_DBError тестирует ошибку БД при получении заказа.
func TestOrderService_GetOrder_DBError(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	mockRepo.On("GetByID", mock.Anything, "order-123").
		Return(nil, errors.New("connection refused"))

	svc := NewOrderService(mockRepo)

	order, err := svc.GetOrder(context.Background(), "order-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, order)

	mockRepo.AssertExpectations(t)
}

// =====================================
// Тесты ListOrders
// =====================================

// TestOrderService_ListOrders тестирует получение списка заказов с пагинацией.
func TestOrderService_ListOrders(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	orders := []*domain.Order{
		{ID: "order-1", UserID: "user-123", Status: domain.OrderStatusPending},
		{ID: "order-2", UserID: "user-123", Status: domain.OrderStatusConfirmed},
	}

	// page=1, pageSize=10 => offset=0, limit=10
	mockRepo.On("ListByUserID", mock.Anything, "user-123", (*domain.OrderStatus)(nil), 0, 10).
		Return(orders, int64(15), nil)

	svc := NewOrderService(mockRepo)

	result, total, err := svc.ListOrders(context.Background(), "user-123", nil, 1, 10)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(15), total)

	mockRepo.AssertExpectations(t)
}

// TestOrderService_ListOrders_WithStatusFilter тестирует фильтрацию по статусу.
func TestOrderService_ListOrders_WithStatusFilter(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	pendingStatus := domain.OrderStatusPending
	orders := []*domain.Order{
		{ID: "order-1", UserID: "user-123", Status: domain.OrderStatusPending},
	}

	mockRepo.On("ListByUserID", mock.Anything, "user-123", &pendingStatus, 0, 20).
		Return(orders, int64(1), nil)

	svc := NewOrderService(mockRepo)

	result, total, err := svc.ListOrders(context.Background(), "user-123", &pendingStatus, 1, 20)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(1), total)

	mockRepo.AssertExpectations(t)
}

// TestOrderService_ListOrders_Pagination тестирует корректную нормализацию параметров пагинации.
func TestOrderService_ListOrders_Pagination(t *testing.T) {
	tests := []struct {
		name           string
		page           int
		pageSize       int
		expectedOffset int
		expectedLimit  int
	}{
		{
			name:           "стандартные параметры",
			page:           2,
			pageSize:       10,
			expectedOffset: 10, // (2-1) * 10
			expectedLimit:  10,
		},
		{
			name:           "отрицательная страница -> page=1",
			page:           -1,
			pageSize:       10,
			expectedOffset: 0, // (1-1) * 10
			expectedLimit:  10,
		},
		{
			name:           "нулевая страница -> page=1",
			page:           0,
			pageSize:       10,
			expectedOffset: 0,
			expectedLimit:  10,
		},
		{
			name:           "нулевой размер страницы -> default=20",
			page:           1,
			pageSize:       0,
			expectedOffset: 0,
			expectedLimit:  20,
		},
		{
			name:           "размер страницы > 100 -> max=100",
			page:           1,
			pageSize:       200,
			expectedOffset: 0,
			expectedLimit:  100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockOrderRepository)

			mockRepo.On("ListByUserID", mock.Anything, "user-123", (*domain.OrderStatus)(nil), tt.expectedOffset, tt.expectedLimit).
				Return([]*domain.Order{}, int64(0), nil)

			svc := NewOrderService(mockRepo)

			_, _, err := svc.ListOrders(context.Background(), "user-123", nil, tt.page, tt.pageSize)

			require.NoError(t, err)
			mockRepo.AssertExpectations(t)
		})
	}
}

// TestOrderService_ListOrders_DBError тестирует ошибку БД при получении списка.
func TestOrderService_ListOrders_DBError(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	mockRepo.On("ListByUserID", mock.Anything, "user-123", (*domain.OrderStatus)(nil), 0, 20).
		Return(nil, int64(0), errors.New("database error"))

	svc := NewOrderService(mockRepo)

	result, total, err := svc.ListOrders(context.Background(), "user-123", nil, 1, 20)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database error")
	assert.Nil(t, result)
	assert.Equal(t, int64(0), total)

	mockRepo.AssertExpectations(t)
}

// =====================================
// Тесты CancelOrder
// =====================================

// TestOrderService_CancelOrder тестирует успешную отмену заказа.
func TestOrderService_CancelOrder(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	pendingOrder := &domain.Order{
		ID:     "order-123",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	mockRepo.On("GetByID", mock.Anything, "order-123").Return(pendingOrder, nil)
	// failure_reason = nil, так как это отмена пользователем, а не системный сбой
	mockRepo.On("UpdateStatus", mock.Anything, "order-123", domain.OrderStatusCancelled, (*string)(nil), (*string)(nil)).
		Return(nil)

	svc := NewOrderService(mockRepo)

	err := svc.CancelOrder(context.Background(), "order-123")

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

// TestOrderService_CancelOrder_NotFound тестирует отмену несуществующего заказа.
func TestOrderService_CancelOrder_NotFound(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	mockRepo.On("GetByID", mock.Anything, "non-existent-order").
		Return(nil, domain.ErrOrderNotFound)

	svc := NewOrderService(mockRepo)

	err := svc.CancelOrder(context.Background(), "non-existent-order")

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrOrderNotFound)

	mockRepo.AssertExpectations(t)
}

// TestOrderService_CancelOrder_WrongStatus тестирует попытку отменить заказ в неподходящем статусе.
func TestOrderService_CancelOrder_WrongStatus(t *testing.T) {
	tests := []struct {
		name   string
		status domain.OrderStatus
	}{
		{
			name:   "CONFIRMED - нельзя отменить",
			status: domain.OrderStatusConfirmed,
		},
		{
			name:   "CANCELLED - нельзя отменить повторно",
			status: domain.OrderStatusCancelled,
		},
		{
			name:   "FAILED - нельзя отменить",
			status: domain.OrderStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockOrderRepository)

			order := &domain.Order{
				ID:     "order-123",
				UserID: "user-123",
				Status: tt.status,
			}

			mockRepo.On("GetByID", mock.Anything, "order-123").Return(order, nil)

			svc := NewOrderService(mockRepo)

			err := svc.CancelOrder(context.Background(), "order-123")

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrOrderCannotCancel)

			// UpdateStatus НЕ должен вызываться
			mockRepo.AssertNotCalled(t, "UpdateStatus")
		})
	}
}

// TestOrderService_CancelOrder_DBError тестирует ошибку БД при отмене заказа.
func TestOrderService_CancelOrder_DBError(t *testing.T) {
	mockRepo := new(MockOrderRepository)

	pendingOrder := &domain.Order{
		ID:     "order-123",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	mockRepo.On("GetByID", mock.Anything, "order-123").Return(pendingOrder, nil)
	mockRepo.On("UpdateStatus", mock.Anything, "order-123", domain.OrderStatusCancelled, (*string)(nil), (*string)(nil)).
		Return(errors.New("database error"))

	svc := NewOrderService(mockRepo)

	err := svc.CancelOrder(context.Background(), "order-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database error")

	mockRepo.AssertExpectations(t)
}
