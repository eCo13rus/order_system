// Package domain содержит unit тесты для доменных сущностей Order Service.
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// =====================================
// Тесты Order.Validate
// =====================================

// TestOrder_Validate тестирует валидацию заказа.
func TestOrder_Validate(t *testing.T) {
	tests := []struct {
		name        string
		order       *Order
		expectedErr error
	}{
		{
			name: "валидные данные",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "user-uuid-123",
				Items: []OrderItem{
					{
						ProductID:   "product-123",
						ProductName: "Товар 1",
						Quantity:    2,
						UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "пустой UserID",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "",
				Items: []OrderItem{
					{
						ProductID:   "product-123",
						ProductName: "Товар 1",
						Quantity:    2,
						UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
					},
				},
			},
			expectedErr: ErrInvalidUserID,
		},
		{
			name: "UserID только пробелы",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "   ",
				Items: []OrderItem{
					{
						ProductID:   "product-123",
						ProductName: "Товар 1",
						Quantity:    2,
						UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
					},
				},
			},
			expectedErr: ErrInvalidUserID,
		},
		{
			name: "пустой список позиций",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "user-uuid-123",
				Items:  []OrderItem{},
			},
			expectedErr: ErrEmptyOrderItems,
		},
		{
			name: "nil список позиций",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "user-uuid-123",
				Items:  nil,
			},
			expectedErr: ErrEmptyOrderItems,
		},
		{
			name: "невалидная позиция - пустой ProductID",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "user-uuid-123",
				Items: []OrderItem{
					{
						ProductID:   "",
						ProductName: "Товар 1",
						Quantity:    2,
						UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
					},
				},
			},
			expectedErr: ErrInvalidProductID,
		},
		{
			name: "невалидная позиция - нулевое количество",
			order: &Order{
				ID:     "order-uuid-123",
				UserID: "user-uuid-123",
				Items: []OrderItem{
					{
						ProductID:   "product-123",
						ProductName: "Товар 1",
						Quantity:    0,
						UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
					},
				},
			},
			expectedErr: ErrInvalidQuantity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.order.Validate()
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =====================================
// Тесты Order.CalculateTotal
// =====================================

// TestOrder_CalculateTotal тестирует расчёт суммы заказа из позиций.
func TestOrder_CalculateTotal(t *testing.T) {
	tests := []struct {
		name             string
		items            []OrderItem
		expectedAmount   int64
		expectedCurrency string
	}{
		{
			name: "одна позиция",
			items: []OrderItem{
				{
					ProductID:   "product-1",
					ProductName: "Товар 1",
					Quantity:    3,
					UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
				},
			},
			expectedAmount:   3000, // 3 * 1000
			expectedCurrency: "RUB",
		},
		{
			name: "несколько позиций",
			items: []OrderItem{
				{
					ProductID:   "product-1",
					ProductName: "Товар 1",
					Quantity:    2,
					UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
				},
				{
					ProductID:   "product-2",
					ProductName: "Товар 2",
					Quantity:    1,
					UnitPrice:   Money{Amount: 500, Currency: "RUB"},
				},
			},
			expectedAmount:   2500, // 2*1000 + 1*500
			expectedCurrency: "RUB",
		},
		{
			name:             "пустой список позиций",
			items:            []OrderItem{},
			expectedAmount:   0,
			expectedCurrency: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &Order{Items: tt.items}
			order.CalculateTotal()

			assert.Equal(t, tt.expectedAmount, order.TotalAmount.Amount)
			assert.Equal(t, tt.expectedCurrency, order.TotalAmount.Currency)
		})
	}
}

// =====================================
// Тесты Order.Cancel
// =====================================

// TestOrder_Cancel тестирует отмену заказа.
func TestOrder_Cancel(t *testing.T) {
	tests := []struct {
		name           string
		status         OrderStatus
		expectedErr    error
		expectedStatus OrderStatus
	}{
		{
			name:           "успешная отмена PENDING",
			status:         OrderStatusPending,
			expectedErr:    nil,
			expectedStatus: OrderStatusCancelled,
		},
		{
			name:           "ошибка отмены CONFIRMED",
			status:         OrderStatusConfirmed,
			expectedErr:    ErrOrderCannotCancel,
			expectedStatus: OrderStatusConfirmed,
		},
		{
			name:           "ошибка отмены CANCELLED",
			status:         OrderStatusCancelled,
			expectedErr:    ErrOrderCannotCancel,
			expectedStatus: OrderStatusCancelled,
		},
		{
			name:           "ошибка отмены FAILED",
			status:         OrderStatusFailed,
			expectedErr:    ErrOrderCannotCancel,
			expectedStatus: OrderStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &Order{Status: tt.status}
			err := order.Cancel()

			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedStatus, order.Status)
		})
	}
}

// =====================================
// Тесты OrderItem.Validate
// =====================================

// TestOrderItem_Validate тестирует валидацию позиции заказа.
func TestOrderItem_Validate(t *testing.T) {
	tests := []struct {
		name        string
		item        *OrderItem
		expectedErr error
	}{
		{
			name: "валидные данные",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: nil,
		},
		{
			name: "пустой ProductID",
			item: &OrderItem{
				ProductID:   "",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: ErrInvalidProductID,
		},
		{
			name: "ProductID только пробелы",
			item: &OrderItem{
				ProductID:   "   ",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: ErrInvalidProductID,
		},
		{
			name: "пустое название товара",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "",
				Quantity:    2,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: ErrInvalidProductName,
		},
		{
			name: "название товара только пробелы",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "   ",
				Quantity:    2,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: ErrInvalidProductName,
		},
		{
			name: "нулевое количество",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "Товар 1",
				Quantity:    0,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: ErrInvalidQuantity,
		},
		{
			name: "отрицательное количество",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "Товар 1",
				Quantity:    -1,
				UnitPrice:   Money{Amount: 1000, Currency: "RUB"},
			},
			expectedErr: ErrInvalidQuantity,
		},
		{
			name: "нулевая цена",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   Money{Amount: 0, Currency: "RUB"},
			},
			expectedErr: ErrInvalidPrice,
		},
		{
			name: "отрицательная цена",
			item: &OrderItem{
				ProductID:   "product-123",
				ProductName: "Товар 1",
				Quantity:    2,
				UnitPrice:   Money{Amount: -100, Currency: "RUB"},
			},
			expectedErr: ErrInvalidPrice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.item.Validate()
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =====================================
// Тесты OrderItem.Total
// =====================================

// TestOrderItem_Total тестирует расчёт стоимости позиции.
func TestOrderItem_Total(t *testing.T) {
	tests := []struct {
		name           string
		quantity       int32
		unitPrice      int64
		currency       string
		expectedAmount int64
	}{
		{
			name:           "стандартный расчёт",
			quantity:       3,
			unitPrice:      1000,
			currency:       "RUB",
			expectedAmount: 3000,
		},
		{
			name:           "одна единица товара",
			quantity:       1,
			unitPrice:      500,
			currency:       "USD",
			expectedAmount: 500,
		},
		{
			name:           "большое количество",
			quantity:       100,
			unitPrice:      250,
			currency:       "EUR",
			expectedAmount: 25000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &OrderItem{
				Quantity:  tt.quantity,
				UnitPrice: Money{Amount: tt.unitPrice, Currency: tt.currency},
			}

			total := item.Total()

			assert.Equal(t, tt.expectedAmount, total.Amount)
			assert.Equal(t, tt.currency, total.Currency)
		})
	}
}

// =====================================
// Тесты Money.Multiply
// =====================================

// TestMoney_Multiply тестирует умножение денежной суммы на количество.
func TestMoney_Multiply(t *testing.T) {
	tests := []struct {
		name           string
		amount         int64
		quantity       int32
		currency       string
		expectedAmount int64
	}{
		{
			name:           "стандартное умножение",
			amount:         1000,
			quantity:       3,
			currency:       "RUB",
			expectedAmount: 3000,
		},
		{
			name:           "умножение на 1",
			amount:         500,
			quantity:       1,
			currency:       "USD",
			expectedAmount: 500,
		},
		{
			name:           "умножение на 0",
			amount:         1000,
			quantity:       0,
			currency:       "RUB",
			expectedAmount: 0,
		},
		{
			name:           "большое количество",
			amount:         100,
			quantity:       1000,
			currency:       "EUR",
			expectedAmount: 100000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Money{Amount: tt.amount, Currency: tt.currency}

			result := m.Multiply(tt.quantity)

			assert.Equal(t, tt.expectedAmount, result.Amount)
			assert.Equal(t, tt.currency, result.Currency)
		})
	}
}

// =====================================
// Тесты Order.Confirm и Order.Fail
// =====================================

// TestOrder_Confirm тестирует подтверждение заказа.
func TestOrder_Confirm(t *testing.T) {
	order := &Order{
		ID:     "order-123",
		Status: OrderStatusPending,
	}

	order.Confirm("payment-123")

	assert.Equal(t, OrderStatusConfirmed, order.Status)
	assert.NotNil(t, order.PaymentID)
	assert.Equal(t, "payment-123", *order.PaymentID)
}

// TestOrder_Fail тестирует пометку заказа как неудачного.
func TestOrder_Fail(t *testing.T) {
	order := &Order{
		ID:     "order-123",
		Status: OrderStatusPending,
	}

	order.Fail("платёж отклонён")

	assert.Equal(t, OrderStatusFailed, order.Status)
	assert.NotNil(t, order.FailureReason)
	assert.Equal(t, "платёж отклонён", *order.FailureReason)
}
