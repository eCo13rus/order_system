package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"example.com/order-system/services/order/internal/domain"
)

// Моки определены в mocks_test.go

// =============================================================================
// Тесты Orchestrator
// =============================================================================

func TestOrchestrator_CreateOrderWithSaga_Success(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	order := &domain.Order{
		ID:     "order-123",
		UserID: "user-456",
		TotalAmount: domain.Money{
			Amount:   10000,
			Currency: "RUB",
		},
		Status:    domain.OrderStatusPending,
		CreatedAt: time.Now(),
	}

	// Ожидаем атомарное создание order + saga + outbox
	sagaRepo.On("CreateOrderWithSagaAndOutbox", ctx, order, mock.AnythingOfType("*saga.Saga"), mock.AnythingOfType("*saga.Outbox")).
		Run(func(args mock.Arguments) {
			saga := args.Get(2).(*Saga)
			assert.Equal(t, StatusPaymentPending, saga.Status)
			assert.Equal(t, "order-123", saga.OrderID)

			outbox := args.Get(3).(*Outbox)
			assert.Equal(t, "order-123", outbox.AggregateID)
			assert.Equal(t, string(CommandProcessPayment), outbox.EventType)
		}).
		Return(nil)

	err := orch.CreateOrderWithSaga(ctx, order)

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
}

func TestOrchestrator_CreateOrderWithSaga_Error(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	order := &domain.Order{
		ID:          "order-123",
		TotalAmount: domain.Money{Amount: 10000, Currency: "RUB"},
	}

	// Ошибка при атомарном создании
	sagaRepo.On("CreateOrderWithSagaAndOutbox", ctx, order, mock.Anything, mock.Anything).
		Return(errors.New("db error"))

	err := orch.CreateOrderWithSaga(ctx, order)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка создания заказа")
}

func TestOrchestrator_HandlePaymentReply_Success(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	// Order в статусе PENDING — будет подтверждён
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
		Timestamp: time.Now(),
	}

	// Ожидаем вызовы
	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	// Атомарное обновление саги и заказа
	sagaRepo.On("UpdateWithOrder", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusConfirmed, mock.AnythingOfType("*string"), (*string)(nil)).Return(nil)

	err := orch.HandlePaymentReply(ctx, reply)

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

func TestOrchestrator_HandlePaymentReply_Failed(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	// Order в статусе PENDING — будет помечен как FAILED
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplyFailed,
		Error:     "Недостаточно средств",
		Timestamp: time.Now(),
	}

	// Ожидаем вызовы
	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	// Атомарное обновление саги, заказа и outbox (outbox = nil, т.к. нет PaymentID)
	sagaRepo.On("UpdateWithOrderAndOutbox", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusFailed, (*string)(nil), mock.AnythingOfType("*string"), (*Outbox)(nil)).Return(nil)

	err := orch.HandlePaymentReply(ctx, reply)

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

func TestOrchestrator_HandlePaymentReply_SagaNotFound(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	reply := &Reply{
		SagaID:  "saga-123",
		OrderID: "order-456",
		Status:  ReplySuccess,
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(nil, ErrSagaNotFound)

	err := orch.HandlePaymentReply(ctx, reply)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "не найдена")
}

func TestOrchestrator_HandlePaymentReply_WrongStatus(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	// Сага уже завершена
	saga := &Saga{
		ID:      "saga-123",
		OrderID: "order-456",
		Status:  StatusCompleted,
	}

	reply := &Reply{
		SagaID:  "saga-123",
		OrderID: "order-456",
		Status:  ReplySuccess,
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)

	// Не должно быть ошибки, просто пропускаем
	err := orch.HandlePaymentReply(ctx, reply)

	require.NoError(t, err)
	// Update не должен вызываться
	sagaRepo.AssertNotCalled(t, "Update")
}

func TestOrchestrator_CompensateSaga_Success(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:      "saga-123",
		OrderID: "order-456",
		Status:  StatusPaymentPending,
	}

	// Order в статусе PENDING — будет помечен как FAILED
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	// Атомарное обновление саги, заказа и outbox (outbox = nil, т.к. CompensateSaga без refund)
	sagaRepo.On("UpdateWithOrderAndOutbox", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusFailed, (*string)(nil), mock.AnythingOfType("*string"), (*Outbox)(nil)).Return(nil)

	err := orch.CompensateSaga(ctx, "saga-123", "Тестовая ошибка")

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

func TestOrchestrator_CompensateSaga_SagaNotFound(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	sagaRepo.On("GetByID", ctx, "saga-123").Return(nil, ErrSagaNotFound)

	err := orch.CompensateSaga(ctx, "saga-123", "Тестовая ошибка")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "не найдена")
}

// TestOrchestrator_HandlePaymentReply_FailedWithRefund тестирует компенсацию с refund.
// Когда Payment Service возвращает FAILED с PaymentID — нужно создать RefundPayment в outbox.
// КРИТИЧНО: refund создаётся АТОМАРНО с обновлением saga и order (предотвращает дублирование при retry).
func TestOrchestrator_HandlePaymentReply_FailedWithRefund(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	// Order в статусе PENDING — будет помечен как FAILED
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	// Reply с PaymentID — значит платёж был создан и нужен refund
	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplyFailed,
		PaymentID: "payment-789", // Есть PaymentID!
		Error:     "Недостаточно средств",
		Timestamp: time.Now(),
	}

	// Ожидаем вызовы
	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	// Атомарное обновление саги, заказа И создание refund outbox в одной транзакции
	sagaRepo.On("UpdateWithOrderAndOutbox", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusFailed, (*string)(nil), mock.AnythingOfType("*string"), mock.AnythingOfType("*saga.Outbox")).
		Run(func(args mock.Arguments) {
			// Проверяем, что outbox для refund передан и корректен
			outbox := args.Get(6).(*Outbox)
			assert.NotNil(t, outbox, "Outbox для refund должен быть создан")
			assert.Equal(t, string(CommandRefundPayment), outbox.EventType)
			assert.Equal(t, "order-456", outbox.AggregateID)
		}).
		Return(nil)

	err := orch.HandlePaymentReply(ctx, reply)

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

// TestOrchestrator_HandlePaymentReply_FailedWithoutRefund тестирует компенсацию без refund.
// Когда Payment Service возвращает FAILED без PaymentID — refund не нужен.
func TestOrchestrator_HandlePaymentReply_FailedWithoutRefund(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	// Order в статусе PENDING — будет помечен как FAILED
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	// Reply БЕЗ PaymentID — платёж не был создан
	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplyFailed,
		PaymentID: "", // Пустой!
		Error:     "Карта заблокирована",
		Timestamp: time.Now(),
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	// Атомарное обновление саги, заказа (outbox = nil, т.к. нет PaymentID)
	sagaRepo.On("UpdateWithOrderAndOutbox", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusFailed, (*string)(nil), mock.AnythingOfType("*string"), (*Outbox)(nil)).Return(nil)

	err := orch.HandlePaymentReply(ctx, reply)

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

// =============================================================================
// Тест защиты от panic (nil StepData)
// =============================================================================

// TestOrchestrator_HandlePaymentReply_FailedWithRefund_NilStepData проверяет защиту от panic.
// Если StepData == nil, buildRefundOutbox вернёт ошибку, но компенсация продолжится без refund.
func TestOrchestrator_HandlePaymentReply_FailedWithRefund_NilStepData(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	// Сага БЕЗ StepData — edge case, ранее вызывал panic
	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: nil, // nil!
	}

	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplyFailed,
		PaymentID: "payment-789", // Есть PaymentID, но StepData = nil
		Error:     "Ошибка оплаты",
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	// Атомарное обновление саги, заказа (outbox = nil, т.к. buildRefundOutbox вернёт ошибку)
	sagaRepo.On("UpdateWithOrderAndOutbox", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusFailed, (*string)(nil), mock.AnythingOfType("*string"), (*Outbox)(nil)).Return(nil)

	// Компенсация должна продолжиться, несмотря на ошибку создания refund outbox
	err := orch.HandlePaymentReply(ctx, reply)

	require.NoError(t, err)
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

// =============================================================================
// Тесты веток ошибок handlePaymentSuccess
// =============================================================================

// TestOrchestrator_HandlePaymentSuccess_OrderNotFound — ошибка при получении заказа.
func TestOrchestrator_HandlePaymentSuccess_OrderNotFound(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
		Timestamp: time.Now(),
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(nil, domain.ErrOrderNotFound)

	err := orch.HandlePaymentReply(ctx, reply)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка получения заказа")
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

// TestOrchestrator_HandlePaymentSuccess_OrderConfirmError — заказ не в статусе PENDING.
func TestOrchestrator_HandlePaymentSuccess_OrderConfirmError(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	// Заказ уже CONFIRMED — Confirm() вернёт ошибку
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusConfirmed,
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
		Timestamp: time.Now(),
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)

	err := orch.HandlePaymentReply(ctx, reply)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка подтверждения заказа")
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

// TestOrchestrator_HandlePaymentSuccess_UpdateError — ошибка при атомарном обновлении.
func TestOrchestrator_HandlePaymentSuccess_UpdateError(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
		Timestamp: time.Now(),
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	sagaRepo.On("UpdateWithOrder", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusConfirmed, mock.AnythingOfType("*string"), (*string)(nil)).
		Return(errors.New("db connection lost"))

	err := orch.HandlePaymentReply(ctx, reply)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка обновления")
	sagaRepo.AssertExpectations(t)
	orderRepo.AssertExpectations(t)
}

// TestOrchestrator_HandlePaymentSuccess_SagaCompleteError — сага уже в терминальном состоянии.
func TestOrchestrator_HandlePaymentSuccess_SagaCompleteError(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	// Сага в COMPLETED — Complete() вернёт ErrSagaCompleted
	// Но HandlePaymentReply проверяет Status раньше и пропустит
	// Поэтому тестируем через StatusCompensating — переход в COMPLETED недопустим
	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusCompensating, // Нельзя перейти в COMPLETED
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
		Timestamp: time.Now(),
	}

	// HandlePaymentReply проверит Status != PAYMENT_PENDING и пропустит
	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)

	err := orch.HandlePaymentReply(ctx, reply)

	// Не ошибка — просто пропускаем ответ
	require.NoError(t, err)
	// Update не вызывается
	sagaRepo.AssertNotCalled(t, "UpdateWithOrder")
}

// =============================================================================
// Тесты веток ошибок compensateSagaWithRefund
// =============================================================================

// TestOrchestrator_CompensateSaga_OrderNotFound — заказ не найден при компенсации.
func TestOrchestrator_CompensateSaga_OrderNotFound(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(nil, domain.ErrOrderNotFound)

	err := orch.CompensateSaga(ctx, "saga-123", "Тестовая ошибка")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка получения заказа")
}

// TestOrchestrator_CompensateSaga_OrderFailError — заказ не в статусе для Fail.
func TestOrchestrator_CompensateSaga_OrderFailError(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	// Заказ уже FAILED — Fail() вернёт ошибку
	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusFailed,
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)

	err := orch.CompensateSaga(ctx, "saga-123", "Тестовая ошибка")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка пометки заказа")
}

// TestOrchestrator_CompensateSaga_UpdateError — ошибка при атомарном обновлении.
func TestOrchestrator_CompensateSaga_UpdateError(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	order := &domain.Order{
		ID:     "order-456",
		UserID: "user-123",
		Status: domain.OrderStatusPending,
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)
	orderRepo.On("GetByID", ctx, "order-456").Return(order, nil)
	sagaRepo.On("UpdateWithOrderAndOutbox", ctx, mock.AnythingOfType("*saga.Saga"), "order-456", domain.OrderStatusFailed, (*string)(nil), mock.AnythingOfType("*string"), (*Outbox)(nil)).
		Return(errors.New("db connection lost"))

	err := orch.CompensateSaga(ctx, "saga-123", "Тестовая ошибка")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка обновления")
}

// TestOrchestrator_CompensateSaga_SagaFailError — сага уже в терминальном состоянии.
func TestOrchestrator_CompensateSaga_SagaFailError(t *testing.T) {
	ctx := context.Background()
	sagaRepo := new(MockSagaRepository)
	orderRepo := new(MockOrderRepository)

	orch := NewOrchestrator(sagaRepo, orderRepo)

	// Сага уже COMPLETED — Fail() вернёт ошибку
	saga := &Saga{
		ID:       "saga-123",
		OrderID:  "order-456",
		Status:   StatusCompleted, // Терминальное состояние
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	sagaRepo.On("GetByID", ctx, "saga-123").Return(saga, nil)

	err := orch.CompensateSaga(ctx, "saga-123", "Тестовая ошибка")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка перехода состояния")
	// OrderRepo не должен вызываться
	orderRepo.AssertNotCalled(t, "GetByID")
}
