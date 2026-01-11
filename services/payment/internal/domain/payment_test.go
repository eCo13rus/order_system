package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// State Machine тесты
// =============================================================================

func TestPayment_IsTerminal(t *testing.T) {
	tests := []struct {
		status   PaymentStatus
		terminal bool
	}{
		{PaymentStatusPending, false},
		{PaymentStatusCompleted, false}, // COMPLETED не терминальный — можно перейти в REFUNDED
		{PaymentStatusFailed, true},
		{PaymentStatusRefunded, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.terminal, tt.status.IsTerminal())
		})
	}
}

func TestPayment_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name      string
		from      PaymentStatus
		to        PaymentStatus
		canChange bool
	}{
		// Из PENDING
		{"PENDING -> COMPLETED", PaymentStatusPending, PaymentStatusCompleted, true},
		{"PENDING -> FAILED", PaymentStatusPending, PaymentStatusFailed, true},
		{"PENDING -> REFUNDED", PaymentStatusPending, PaymentStatusRefunded, false},
		{"PENDING -> PENDING", PaymentStatusPending, PaymentStatusPending, false},

		// Из COMPLETED
		{"COMPLETED -> REFUNDED", PaymentStatusCompleted, PaymentStatusRefunded, true},
		{"COMPLETED -> FAILED", PaymentStatusCompleted, PaymentStatusFailed, false},
		{"COMPLETED -> PENDING", PaymentStatusCompleted, PaymentStatusPending, false},

		// Из терминальных состояний
		{"FAILED -> любой", PaymentStatusFailed, PaymentStatusCompleted, false},
		{"REFUNDED -> любой", PaymentStatusRefunded, PaymentStatusCompleted, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Payment{Status: tt.from}
			assert.Equal(t, tt.canChange, p.CanTransitionTo(tt.to))
		})
	}
}

func TestPayment_Complete(t *testing.T) {
	t.Run("успешный переход из PENDING", func(t *testing.T) {
		p := newTestPayment(PaymentStatusPending)

		err := p.Complete()

		require.NoError(t, err)
		assert.Equal(t, PaymentStatusCompleted, p.Status)
	})

	t.Run("ошибка из FAILED", func(t *testing.T) {
		p := newTestPayment(PaymentStatusFailed)

		err := p.Complete()

		require.Error(t, err)
		assert.Equal(t, PaymentStatusFailed, p.Status) // Статус не изменился
	})

	t.Run("ошибка из COMPLETED", func(t *testing.T) {
		p := newTestPayment(PaymentStatusCompleted)

		err := p.Complete()

		require.Error(t, err)
	})
}

func TestPayment_Fail(t *testing.T) {
	t.Run("успешный переход из PENDING", func(t *testing.T) {
		p := newTestPayment(PaymentStatusPending)
		reason := "недостаточно средств"

		err := p.Fail(reason)

		require.NoError(t, err)
		assert.Equal(t, PaymentStatusFailed, p.Status)
		require.NotNil(t, p.FailureReason)
		assert.Equal(t, reason, *p.FailureReason)
	})

	t.Run("ошибка из COMPLETED", func(t *testing.T) {
		p := newTestPayment(PaymentStatusCompleted)

		err := p.Fail("тест")

		require.Error(t, err)
		assert.Equal(t, PaymentStatusCompleted, p.Status)
	})
}

func TestPayment_Refund(t *testing.T) {
	t.Run("успешный возврат из COMPLETED", func(t *testing.T) {
		p := newTestPayment(PaymentStatusCompleted)
		refundID := "refund-123"
		reason := "по запросу клиента"

		err := p.Refund(refundID, reason)

		require.NoError(t, err)
		assert.Equal(t, PaymentStatusRefunded, p.Status)
		require.NotNil(t, p.RefundID)
		assert.Equal(t, refundID, *p.RefundID)
		require.NotNil(t, p.RefundReason)
		assert.Equal(t, reason, *p.RefundReason)
	})

	t.Run("ошибка возврата из PENDING", func(t *testing.T) {
		p := newTestPayment(PaymentStatusPending)

		err := p.Refund("refund-123", "тест")

		require.Error(t, err)
		assert.Equal(t, PaymentStatusPending, p.Status)
	})

	t.Run("ошибка возврата из FAILED", func(t *testing.T) {
		p := newTestPayment(PaymentStatusFailed)

		err := p.Refund("refund-123", "тест")

		require.Error(t, err)
	})
}

// =============================================================================
// Validation тесты
// =============================================================================

func TestPayment_Validate(t *testing.T) {
	tests := []struct {
		name    string
		payment *Payment
		wantErr bool
	}{
		{
			name:    "валидный платёж",
			payment: newTestPayment(PaymentStatusPending),
			wantErr: false,
		},
		{
			name: "пустой order_id",
			payment: &Payment{
				SagaID:   "saga-123",
				Amount:   1000,
				Currency: "RUB",
			},
			wantErr: true,
		},
		{
			name: "пустой saga_id",
			payment: &Payment{
				OrderID:  "order-123",
				Amount:   1000,
				Currency: "RUB",
			},
			wantErr: true,
		},
		{
			name: "нулевая сумма",
			payment: &Payment{
				OrderID:  "order-123",
				SagaID:   "saga-123",
				Amount:   0,
				Currency: "RUB",
			},
			wantErr: true,
		},
		{
			name: "отрицательная сумма",
			payment: &Payment{
				OrderID:  "order-123",
				SagaID:   "saga-123",
				Amount:   -100,
				Currency: "RUB",
			},
			wantErr: true,
		},
		{
			name: "пустой user_id",
			payment: &Payment{
				OrderID:  "order-123",
				SagaID:   "saga-123",
				Amount:   1000,
				Currency: "RUB",
			},
			wantErr: true,
		},
		{
			name: "пустая валюта",
			payment: &Payment{
				OrderID: "order-123",
				SagaID:  "saga-123",
				UserID:  "user-123",
				Amount:  1000,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payment.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// Helpers
// =============================================================================

// newTestPayment создаёт тестовый платёж.
func newTestPayment(status PaymentStatus) *Payment {
	return &Payment{
		ID:             "payment-test-123",
		OrderID:        "order-123",
		SagaID:         "saga-123",
		UserID:         "user-123",
		Amount:         10000, // 100.00 RUB
		Currency:       "RUB",
		Status:         status,
		PaymentMethod:  "card",
		IdempotencyKey: "saga-123",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}
