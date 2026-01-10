// Package testutil содержит общие моки и утилиты для тестирования.
// Моки вынесены сюда для избежания дублирования (DRY).
// ВАЖНО: этот пакет НЕ должен импортировать saga (circular dependency).
package testutil

import (
	"context"

	"github.com/stretchr/testify/mock"

	"example.com/order-system/services/order/internal/domain"
)

// =============================================================================
// MockOrderRepository — мок для repository.OrderRepository
// =============================================================================

// MockOrderRepository — мок OrderRepository для unit-тестов.
// Используется в saga и service пакетах.
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
