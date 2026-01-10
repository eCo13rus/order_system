package saga

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Тесты State Machine (переходы состояний)
// =============================================================================

func TestSaga_StatusIsTerminal(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
	}{
		{StatusPaymentPending, false},
		{StatusCompensating, false},
		{StatusCompleted, true},
		{StatusFailed, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.terminal, tt.status.IsTerminal())
		})
	}
}

func TestSaga_CanTransitionTo_ValidTransitions(t *testing.T) {
	tests := []struct {
		name  string
		from  Status
		to    Status
		canDo bool
	}{
		// PAYMENT_PENDING → COMPLETED или COMPENSATING
		{"PAYMENT_PENDING → COMPLETED", StatusPaymentPending, StatusCompleted, true},
		{"PAYMENT_PENDING → COMPENSATING", StatusPaymentPending, StatusCompensating, true},
		{"PAYMENT_PENDING → FAILED", StatusPaymentPending, StatusFailed, false},

		// COMPENSATING → FAILED (единственный допустимый переход)
		{"COMPENSATING → FAILED", StatusCompensating, StatusFailed, true},
		{"COMPENSATING → COMPLETED", StatusCompensating, StatusCompleted, false},
		{"COMPENSATING → PAYMENT_PENDING", StatusCompensating, StatusPaymentPending, false},

		// COMPLETED — терминальное, никуда нельзя
		{"COMPLETED → любой", StatusCompleted, StatusPaymentPending, false},

		// FAILED — терминальное, никуда нельзя
		{"FAILED → любой", StatusFailed, StatusPaymentPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saga := &Saga{Status: tt.from}
			assert.Equal(t, tt.canDo, saga.CanTransitionTo(tt.to))
		})
	}
}

func TestSaga_TransitionTo_Success(t *testing.T) {
	saga := &Saga{
		ID:        "saga-1",
		OrderID:   "order-1",
		Status:    StatusPaymentPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// PAYMENT_PENDING → COMPLETED
	err := saga.TransitionTo(StatusCompleted)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, saga.Status)
}

func TestSaga_TransitionTo_InvalidTransition(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusPaymentPending,
	}

	// Попытка перейти напрямую в FAILED (минуя COMPENSATING)
	err := saga.TransitionTo(StatusFailed)
	assert.ErrorIs(t, err, ErrInvalidTransition)
	assert.Equal(t, StatusPaymentPending, saga.Status) // Состояние не изменилось
}

func TestSaga_TransitionTo_FromTerminalState(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusCompleted,
	}

	// Попытка перейти из терминального состояния
	err := saga.TransitionTo(StatusPaymentPending)
	assert.ErrorIs(t, err, ErrSagaCompleted)
}

// =============================================================================
// Тесты методов переходов
// =============================================================================

func TestSaga_Complete(t *testing.T) {
	saga := &Saga{
		ID:       "saga-1",
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	paymentID := "payment-123"
	err := saga.Complete(paymentID)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, saga.Status)
	assert.Equal(t, paymentID, saga.StepData.PaymentID)
}

func TestSaga_Complete_InvalidState(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusCompensating, // Нельзя перейти в COMPLETED из COMPENSATING
	}

	err := saga.Complete("payment-123")
	assert.Error(t, err)
}

func TestSaga_StartCompensation(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusPaymentPending,
	}

	err := saga.StartCompensation()
	require.NoError(t, err)
	assert.Equal(t, StatusCompensating, saga.Status)
}

func TestSaga_Fail_FromPaymentPending(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusPaymentPending,
	}

	reason := "Недостаточно средств"
	err := saga.Fail(reason)

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, saga.Status)
	require.NotNil(t, saga.FailureReason)
	assert.Equal(t, reason, *saga.FailureReason)
}

func TestSaga_Fail_FromCompensating(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusCompensating,
	}

	reason := "Ошибка при компенсации"
	err := saga.Fail(reason)

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, saga.Status)
	require.NotNil(t, saga.FailureReason)
	assert.Equal(t, reason, *saga.FailureReason)
}

func TestSaga_Fail_InvalidState(t *testing.T) {
	saga := &Saga{
		ID:     "saga-1",
		Status: StatusCompleted, // Нельзя Fail из терминального состояния
	}

	err := saga.Fail("некая ошибка")
	assert.Error(t, err)
}

// =============================================================================
// Тесты Command
// =============================================================================

func TestCommand_ToJSON_FromJSON(t *testing.T) {
	cmd := &Command{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Type:      CommandProcessPayment,
		Amount:    10000,
		Currency:  "RUB",
		Timestamp: time.Date(2025, 12, 30, 12, 0, 0, 0, time.UTC),
	}

	// Сериализация
	data, err := cmd.ToJSON()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Десериализация
	parsed, err := CommandFromJSON(data)
	require.NoError(t, err)

	assert.Equal(t, cmd.SagaID, parsed.SagaID)
	assert.Equal(t, cmd.OrderID, parsed.OrderID)
	assert.Equal(t, cmd.Type, parsed.Type)
	assert.Equal(t, cmd.Amount, parsed.Amount)
	assert.Equal(t, cmd.Currency, parsed.Currency)
}

func TestCommand_FromJSON_InvalidJSON(t *testing.T) {
	_, err := CommandFromJSON([]byte("invalid json"))
	assert.Error(t, err)
}

// =============================================================================
// Тесты Reply
// =============================================================================

func TestReply_ToJSON_FromJSON(t *testing.T) {
	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
		Timestamp: time.Date(2025, 12, 30, 12, 0, 0, 0, time.UTC),
	}

	// Сериализация
	data, err := reply.ToJSON()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Десериализация
	parsed, err := ReplyFromJSON(data)
	require.NoError(t, err)

	assert.Equal(t, reply.SagaID, parsed.SagaID)
	assert.Equal(t, reply.OrderID, parsed.OrderID)
	assert.Equal(t, reply.Status, parsed.Status)
	assert.Equal(t, reply.PaymentID, parsed.PaymentID)
}

func TestReply_IsSuccess(t *testing.T) {
	successReply := &Reply{Status: ReplySuccess}
	failedReply := &Reply{Status: ReplyFailed}

	assert.True(t, successReply.IsSuccess())
	assert.False(t, failedReply.IsSuccess())
}

func TestReply_WithError(t *testing.T) {
	reply := &Reply{
		SagaID:  "saga-123",
		OrderID: "order-456",
		Status:  ReplyFailed,
		Error:   "Недостаточно средств на счёте",
	}

	data, err := reply.ToJSON()
	require.NoError(t, err)

	parsed, err := ReplyFromJSON(data)
	require.NoError(t, err)

	assert.False(t, parsed.IsSuccess())
	assert.Equal(t, "Недостаточно средств на счёте", parsed.Error)
}

// =============================================================================
// Тесты полного flow саги
// =============================================================================

func TestSaga_HappyPath_PaymentToCompleted(t *testing.T) {
	// Сценарий: сага в PAYMENT_PENDING → получаем успешный ответ → COMPLETED
	saga := &Saga{
		ID:        "saga-1",
		OrderID:   "order-1",
		Status:    StatusPaymentPending, // Саги создаются сразу в этом состоянии
		StepData:  &StepData{Amount: 10000, Currency: "RUB"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Получаем успешный ответ от Payment Service
	err := saga.Complete("payment-123")
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, saga.Status)
	assert.Equal(t, "payment-123", saga.StepData.PaymentID)

	// Проверяем, что сага в терминальном состоянии
	assert.True(t, saga.Status.IsTerminal())
}

func TestSaga_FailurePath_PaymentToFailed(t *testing.T) {
	// Сценарий: сага в PAYMENT_PENDING → ошибка оплаты → FAILED
	saga := &Saga{
		ID:        "saga-1",
		OrderID:   "order-1",
		Status:    StatusPaymentPending, // Саги создаются сразу в этом состоянии
		StepData:  &StepData{Amount: 10000, Currency: "RUB"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Получаем ошибку от Payment Service → запускаем компенсацию
	err := saga.Fail("Карта заблокирована")
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, saga.Status)
	require.NotNil(t, saga.FailureReason)
	assert.Equal(t, "Карта заблокирована", *saga.FailureReason)

	// Проверяем, что сага в терминальном состоянии
	assert.True(t, saga.Status.IsTerminal())
}
