// Package domain содержит бизнес-сущности Payment Service.
package domain

import (
	"errors"
	"time"
)

// PaymentStatus — статус платежа в системе.
type PaymentStatus string

const (
	// PaymentStatusPending — платёж создан, ожидает обработки.
	PaymentStatusPending PaymentStatus = "PENDING"

	// PaymentStatusCompleted — платёж успешно завершён.
	PaymentStatusCompleted PaymentStatus = "COMPLETED"

	// PaymentStatusFailed — платёж не прошёл (недостаточно средств, ошибка провайдера).
	PaymentStatusFailed PaymentStatus = "FAILED"

	// PaymentStatusRefunded — платёж возвращён (компенсирующая транзакция).
	PaymentStatusRefunded PaymentStatus = "REFUNDED"
)

// IsTerminal возвращает true, если платёж в финальном состоянии.
// COMPLETED не терминальный — из него возможен переход в REFUNDED.
func (s PaymentStatus) IsTerminal() bool {
	return s == PaymentStatusFailed || s == PaymentStatusRefunded
}

// =============================================================================
// Допустимые переходы состояний (State Machine)
// =============================================================================

// allowedTransitions определяет валидные переходы состояний платежа.
var allowedTransitions = map[PaymentStatus][]PaymentStatus{
	PaymentStatusPending:   {PaymentStatusCompleted, PaymentStatusFailed},
	PaymentStatusCompleted: {PaymentStatusRefunded},
	// PaymentStatusFailed и PaymentStatusRefunded — терминальные состояния
}

// =============================================================================
// Payment — доменная сущность
// =============================================================================

// Payment — платёж в системе.
type Payment struct {
	ID             string        // UUID платежа
	OrderID        string        // ID связанного заказа
	SagaID         string        // ID саги для корреляции
	UserID         string        // ID пользователя
	Amount         int64         // Сумма в минимальных единицах (копейки/центы)
	Currency       string        // ISO 4217 код валюты
	Status         PaymentStatus // Текущий статус
	PaymentMethod  string        // Метод оплаты (card, wallet и т.д.)
	FailureReason  *string       // Причина ошибки (при FAILED)
	RefundID       *string       // ID возврата (при REFUNDED)
	RefundReason   *string       // Причина возврата
	IdempotencyKey string        // Ключ идемпотентности (saga_id)
	CreatedAt      time.Time     // Дата создания
	UpdatedAt      time.Time     // Дата обновления
}

// CanTransitionTo проверяет, допустим ли переход в указанное состояние.
func (p *Payment) CanTransitionTo(newStatus PaymentStatus) bool {
	allowed, ok := allowedTransitions[p.Status]
	if !ok {
		return false // Терминальное состояние
	}
	for _, status := range allowed {
		if status == newStatus {
			return true
		}
	}
	return false
}

// TransitionTo выполняет переход в новое состояние.
func (p *Payment) TransitionTo(newStatus PaymentStatus) error {
	if !p.CanTransitionTo(newStatus) {
		return ErrInvalidTransition
	}
	p.Status = newStatus
	p.UpdatedAt = time.Now()
	return nil
}

// Complete успешно завершает платёж.
func (p *Payment) Complete() error {
	return p.TransitionTo(PaymentStatusCompleted)
}

// Fail помечает платёж как неудачный с указанием причины.
func (p *Payment) Fail(reason string) error {
	if err := p.TransitionTo(PaymentStatusFailed); err != nil {
		return err
	}
	p.FailureReason = &reason
	return nil
}

// Refund выполняет возврат платежа.
func (p *Payment) Refund(refundID, reason string) error {
	if err := p.TransitionTo(PaymentStatusRefunded); err != nil {
		return err
	}
	p.RefundID = &refundID
	p.RefundReason = &reason
	return nil
}

// Validate проверяет корректность полей платежа.
func (p *Payment) Validate() error {
	if p.OrderID == "" {
		return errors.New("order_id обязателен")
	}
	if p.SagaID == "" {
		return errors.New("saga_id обязателен")
	}
	if p.UserID == "" {
		return errors.New("user_id обязателен")
	}
	if p.Amount <= 0 {
		return ErrInvalidAmount
	}
	if p.Currency == "" {
		return errors.New("currency обязательна")
	}
	return nil
}
