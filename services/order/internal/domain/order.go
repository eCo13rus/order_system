// Package domain содержит бизнес-сущности и доменные ошибки Order Service.
package domain

import (
	"strings"
	"time"
)

// OrderStatus — статус заказа в системе.
type OrderStatus string

const (
	// OrderStatusPending — заказ создан, ожидает подтверждения оплаты.
	OrderStatusPending OrderStatus = "PENDING"

	// OrderStatusConfirmed — заказ подтверждён, оплата прошла успешно.
	OrderStatusConfirmed OrderStatus = "CONFIRMED"

	// OrderStatusCancelled — заказ отменён пользователем или системой.
	OrderStatusCancelled OrderStatus = "CANCELLED"

	// OrderStatusFailed — заказ не выполнен из-за ошибки (платёж отклонён и т.д.).
	OrderStatusFailed OrderStatus = "FAILED"
)

// Money — денежная сумма с валютой.
// Хранит сумму в минимальных единицах (копейки, центы) для избежания проблем с плавающей точкой.
type Money struct {
	Currency string // ISO 4217 код валюты (USD, RUB, EUR)
	Amount   int64  // Сумма в минимальных единицах (копейки/центы)
}

// Multiply умножает сумму на количество.
// Используется для расчёта стоимости позиции (цена * количество).
func (m Money) Multiply(quantity int32) Money {
	return Money{
		Currency: m.Currency,
		Amount:   m.Amount * int64(quantity),
	}
}

// Order — заказ в системе.
// Это доменная сущность без зависимостей от инфраструктуры (GORM, proto).
type Order struct {
	ID             string      // Уникальный идентификатор заказа (UUID)
	UserID         string      // ID пользователя, создавшего заказ
	Items          []OrderItem // Позиции заказа
	TotalAmount    Money       // Общая сумма заказа
	Status         OrderStatus // Текущий статус заказа
	PaymentID      *string     // ID платежа (nil если платёж ещё не создан)
	FailureReason  *string     // Причина ошибки (nil если заказ успешен)
	IdempotencyKey string      // Ключ идемпотентности для предотвращения дубликатов
	CreatedAt      time.Time   // Дата создания заказа
	UpdatedAt      time.Time   // Дата последнего обновления
}

// Validate проверяет корректность полей заказа.
// Вызывается перед созданием заказа.
func (o *Order) Validate() error {
	if err := o.validateUserID(); err != nil {
		return err
	}

	if err := o.validateItems(); err != nil {
		return err
	}

	return nil
}

// validateUserID проверяет, что UserID не пустой.
func (o *Order) validateUserID() error {
	if strings.TrimSpace(o.UserID) == "" {
		return ErrInvalidUserID
	}
	return nil
}

// validateItems проверяет, что заказ содержит хотя бы одну позицию.
func (o *Order) validateItems() error {
	if len(o.Items) == 0 {
		return ErrEmptyOrderItems
	}

	for i := range o.Items {
		if err := o.Items[i].Validate(); err != nil {
			return err
		}
	}

	return nil
}

// CalculateTotal пересчитывает общую сумму заказа из позиций.
// Валюта берётся из первой позиции.
func (o *Order) CalculateTotal() {
	if len(o.Items) == 0 {
		o.TotalAmount = Money{Amount: 0}
		return
	}

	// Берём валюту из первой позиции
	currency := o.Items[0].UnitPrice.Currency
	var totalAmount int64

	for i := range o.Items {
		itemTotal := o.Items[i].Total()
		totalAmount += itemTotal.Amount
	}

	o.TotalAmount = Money{
		Currency: currency,
		Amount:   totalAmount,
	}
}

// CanCancel проверяет, можно ли отменить заказ.
// Отменить можно только заказ в статусе PENDING.
func (o *Order) CanCancel() bool {
	return o.Status == OrderStatusPending
}

// Cancel отменяет заказ, если это возможно.
// Возвращает ошибку, если заказ нельзя отменить.
func (o *Order) Cancel() error {
	if !o.CanCancel() {
		return ErrOrderCannotCancel
	}
	o.Status = OrderStatusCancelled
	o.UpdatedAt = time.Now()
	return nil
}

// CanConfirm проверяет, можно ли подтвердить заказ.
// Подтвердить можно только заказ в статусе PENDING.
func (o *Order) CanConfirm() bool {
	return o.Status == OrderStatusPending
}

// Confirm подтверждает заказ после успешной оплаты.
// Возвращает ошибку, если заказ не в статусе PENDING.
func (o *Order) Confirm(paymentID string) error {
	if !o.CanConfirm() {
		return ErrOrderCannotConfirm
	}
	o.Status = OrderStatusConfirmed
	o.PaymentID = &paymentID
	o.UpdatedAt = time.Now()
	return nil
}

// CanFail проверяет, можно ли пометить заказ как failed.
// Пометить как failed можно только заказ в статусе PENDING.
func (o *Order) CanFail() bool {
	return o.Status == OrderStatusPending
}

// Fail помечает заказ как неудачный с указанием причины.
// Возвращает ошибку, если заказ не в статусе PENDING.
func (o *Order) Fail(reason string) error {
	if !o.CanFail() {
		return ErrOrderCannotFail
	}
	o.Status = OrderStatusFailed
	o.FailureReason = &reason
	o.UpdatedAt = time.Now()
	return nil
}

// OrderItem — позиция заказа.
// Содержит информацию о товаре, количестве и цене.
type OrderItem struct {
	ID          string // Уникальный идентификатор позиции (UUID)
	OrderID     string // ID заказа, к которому относится позиция
	ProductID   string // ID товара
	ProductName string // Название товара (денормализовано для истории)
	Quantity    int32  // Количество единиц товара
	UnitPrice   Money  // Цена за единицу товара
}

// Validate проверяет корректность полей позиции заказа.
func (oi *OrderItem) Validate() error {
	if strings.TrimSpace(oi.ProductID) == "" {
		return ErrInvalidProductID
	}

	if strings.TrimSpace(oi.ProductName) == "" {
		return ErrInvalidProductName
	}

	if oi.Quantity <= 0 {
		return ErrInvalidQuantity
	}

	if oi.UnitPrice.Amount <= 0 {
		return ErrInvalidPrice
	}

	return nil
}

// Total возвращает общую стоимость позиции (количество * цена за единицу).
func (oi *OrderItem) Total() Money {
	return oi.UnitPrice.Multiply(oi.Quantity)
}
