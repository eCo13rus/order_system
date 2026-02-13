// Package saga содержит моки для тестирования saga пакета.
// MockOrderRepository вынесен в testutil для DRY.
// MockOrchestrator остаётся здесь (зависит от saga.Reply — избегаем circular import).
package saga

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"example.com/order-system/pkg/kafka"
	outboxpkg "example.com/order-system/pkg/outbox"
	"example.com/order-system/services/order/internal/domain"
	"example.com/order-system/services/order/internal/testutil"
)

// MockOrderRepository — алиас на общий мок из testutil (DRY)
type MockOrderRepository = testutil.MockOrderRepository

// =============================================================================
// MockSagaRepository — мок SagaRepository
// =============================================================================

// MockSagaRepository — мок SagaRepository.
// Реализует только методы из интерфейса (ISP).
type MockSagaRepository struct {
	mock.Mock
}

func (m *MockSagaRepository) GetByID(ctx context.Context, id string) (*Saga, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Saga), args.Error(1)
}

func (m *MockSagaRepository) GetByOrderID(ctx context.Context, orderID string) (*Saga, error) {
	args := m.Called(ctx, orderID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Saga), args.Error(1)
}

func (m *MockSagaRepository) CreateWithOutbox(ctx context.Context, saga *Saga, outbox *outboxpkg.Outbox) error {
	args := m.Called(ctx, saga, outbox)
	return args.Error(0)
}

func (m *MockSagaRepository) CreateOrderWithSagaAndOutbox(ctx context.Context, order *domain.Order, saga *Saga, outbox *outboxpkg.Outbox) error {
	args := m.Called(ctx, order, saga, outbox)
	return args.Error(0)
}

func (m *MockSagaRepository) UpdateWithOrder(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string) error {
	args := m.Called(ctx, saga, orderID, orderStatus, paymentID, failureReason)
	return args.Error(0)
}

func (m *MockSagaRepository) UpdateWithOrderAndOutbox(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string, outbox *outboxpkg.Outbox) error {
	args := m.Called(ctx, saga, orderID, orderStatus, paymentID, failureReason, outbox)
	return args.Error(0)
}

func (m *MockSagaRepository) GetStuckSagas(ctx context.Context, stuckSince time.Time, limit int) ([]*Saga, error) {
	args := m.Called(ctx, stuckSince, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*Saga), args.Error(1)
}

// =============================================================================
// MockOutboxRepository — мок OutboxRepository
// =============================================================================

// MockOutboxRepository — мок OutboxRepository.
type MockOutboxRepository struct {
	mock.Mock
}

func (m *MockOutboxRepository) Create(ctx context.Context, outbox *outboxpkg.Outbox) error {
	args := m.Called(ctx, outbox)
	return args.Error(0)
}

func (m *MockOutboxRepository) GetUnprocessed(ctx context.Context, limit int) ([]*outboxpkg.Outbox, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*outboxpkg.Outbox), args.Error(1)
}

func (m *MockOutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockOutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	args := m.Called(ctx, id, err)
	return args.Error(0)
}

func (m *MockOutboxRepository) DeleteProcessedBefore(ctx context.Context, before time.Time) (int64, error) {
	args := m.Called(ctx, before)
	return args.Get(0).(int64), args.Error(1)
}

// =============================================================================
// MockKafkaProducer — мок KafkaProducer
// =============================================================================

// MockKafkaProducer — мок KafkaProducer.
type MockKafkaProducer struct {
	mock.Mock
}

func (m *MockKafkaProducer) SendMessage(ctx context.Context, msg *kafka.Message) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

// =============================================================================
// MockKafkaConsumer — мок KafkaConsumer
// =============================================================================

// MockKafkaConsumer — мок KafkaConsumer.
type MockKafkaConsumer struct {
	mock.Mock
	capturedHandler kafka.MessageHandler // Захватываем handler для вызова в тестах
}

func (m *MockKafkaConsumer) ConsumeWithRetry(ctx context.Context, handler kafka.MessageHandler, maxRetries int) error {
	args := m.Called(ctx, handler, maxRetries)
	m.capturedHandler = handler // Сохраняем handler для тестирования
	return args.Error(0)
}

func (m *MockKafkaConsumer) Close() error {
	args := m.Called()
	return args.Error(0)
}

// =============================================================================
// MockOrchestrator — мок Orchestrator
// =============================================================================

// MockOrchestrator — мок Orchestrator.
// Остаётся в этом пакете (зависит от saga.Reply — избегаем circular import с testutil).
type MockOrchestrator struct {
	mock.Mock
}

func (m *MockOrchestrator) CreateOrderWithSaga(ctx context.Context, order *domain.Order) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockOrchestrator) HandlePaymentReply(ctx context.Context, reply *Reply) error {
	args := m.Called(ctx, reply)
	return args.Error(0)
}

func (m *MockOrchestrator) CompensateSaga(ctx context.Context, sagaID string, reason string) error {
	args := m.Called(ctx, sagaID, reason)
	return args.Error(0)
}

func (m *MockOrchestrator) IsSagaActive(ctx context.Context, orderID string) (bool, error) {
	args := m.Called(ctx, orderID)
	return args.Bool(0), args.Error(1)
}
