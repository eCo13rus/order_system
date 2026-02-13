// Package saga реализует Saga Orchestration для распределённых транзакций.
// Order Service выступает координатором саги, управляя шагами:
// 1. Создание заказа (PENDING)
// 2. Запрос оплаты → Payment Service
// 3. Подтверждение или откат заказа
package saga

import (
	"errors"
	"time"

	outboxpkg "example.com/order-system/pkg/outbox"
	sagatypes "example.com/order-system/pkg/saga"
)

// =============================================================================
// Состояния Saga
// =============================================================================

// Status — состояние саги в state machine.
type Status string

const (
	// StatusPaymentPending — команда ProcessPayment отправлена в Payment Service.
	// Это начальное состояние саги (создаётся атомарно с outbox записью).
	StatusPaymentPending Status = "PAYMENT_PENDING"

	// StatusCompleted — платёж успешен, заказ подтверждён (CONFIRMED).
	StatusCompleted Status = "COMPLETED"

	// StatusCompensating — получена ошибка, выполняем компенсирующие действия.
	StatusCompensating Status = "COMPENSATING"

	// StatusFailed — сага завершена с ошибкой, заказ помечен как FAILED.
	StatusFailed Status = "FAILED"
)

// IsTerminal возвращает true, если сага в финальном состоянии.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// =============================================================================
// Saga — доменная сущность
// =============================================================================

// Saga — состояние распределённой транзакции.
type Saga struct {
	ID            string    // UUID саги
	OrderID       string    // ID связанного заказа
	Status        Status    // Текущее состояние
	StepData      *StepData // Данные для текущего шага (JSON в БД)
	FailureReason *string   // Причина ошибки (при FAILED)
	Version       int       // Optimistic Locking: инкрементируется при каждом обновлении
	CreatedAt     time.Time // Время создания
	UpdatedAt     time.Time // Время последнего обновления
}

// StepData — данные, необходимые для выполнения шагов саги.
type StepData struct {
	Amount    int64  `json:"amount"`               // Сумма платежа в минимальных единицах
	Currency  string `json:"currency"`             // Валюта (RUB, USD)
	PaymentID string `json:"payment_id,omitempty"` // ID платежа (после успешной оплаты)
}

// =============================================================================
// Переходы состояний (State Machine)
// =============================================================================

// Ошибки переходов состояний.
var (
	ErrInvalidTransition = errors.New("недопустимый переход состояния саги")
	ErrSagaCompleted     = errors.New("сага уже завершена")
)

// allowedTransitions определяет допустимые переходы состояний.
// Ключ — текущее состояние, значение — список допустимых следующих состояний.
var allowedTransitions = map[Status][]Status{
	StatusPaymentPending: {StatusCompleted, StatusCompensating},
	StatusCompensating:   {StatusFailed},
	// StatusCompleted и StatusFailed — терминальные, переходов нет
}

// CanTransitionTo проверяет, допустим ли переход в указанное состояние.
func (s *Saga) CanTransitionTo(newStatus Status) bool {
	allowed, ok := allowedTransitions[s.Status]
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
// Возвращает ошибку, если переход недопустим.
func (s *Saga) TransitionTo(newStatus Status) error {
	if s.Status.IsTerminal() {
		return ErrSagaCompleted
	}

	if !s.CanTransitionTo(newStatus) {
		return ErrInvalidTransition
	}

	s.Status = newStatus
	s.UpdatedAt = time.Now()
	return nil
}

// Complete переводит сагу в состояние COMPLETED после успешной оплаты.
func (s *Saga) Complete(paymentID string) error {
	if err := s.TransitionTo(StatusCompleted); err != nil {
		return err
	}

	// Сохраняем ID платежа
	if s.StepData == nil {
		s.StepData = &StepData{}
	}
	s.StepData.PaymentID = paymentID
	return nil
}

// StartCompensation переводит сагу в состояние COMPENSATING.
func (s *Saga) StartCompensation() error {
	return s.TransitionTo(StatusCompensating)
}

// Fail завершает сагу с ошибкой.
func (s *Saga) Fail(reason string) error {
	// Используем switch для более явного описания переходов (staticcheck QF1003)
	switch s.Status {
	case StatusCompensating:
		// Из COMPENSATING переходим напрямую в FAILED
		if err := s.TransitionTo(StatusFailed); err != nil {
			return err
		}
	case StatusPaymentPending:
		// Из PAYMENT_PENDING сначала в COMPENSATING, затем в FAILED
		if err := s.TransitionTo(StatusCompensating); err != nil {
			return err
		}
		if err := s.TransitionTo(StatusFailed); err != nil {
			return err
		}
	default:
		return ErrInvalidTransition
	}

	s.FailureReason = &reason
	return nil
}

// =============================================================================
// Алиасы типов команд/ответов из pkg/saga (единый источник правды)
// =============================================================================

// Алиасы типов — используются во всём пакете без изменения сигнатур.
type (
	CommandType = sagatypes.CommandType
	Command     = sagatypes.Command
	ReplyStatus = sagatypes.ReplyStatus
	Reply       = sagatypes.Reply
)

// Алиасы констант — для использования без префикса пакета.
const (
	CommandProcessPayment = sagatypes.CommandProcessPayment
	CommandRefundPayment  = sagatypes.CommandRefundPayment
	ReplySuccess          = sagatypes.ReplySuccess
	ReplyFailed           = sagatypes.ReplyFailed
)

// Алиасы функций.
var (
	CommandFromJSON = sagatypes.CommandFromJSON
	ReplyFromJSON   = sagatypes.ReplyFromJSON
)

// =============================================================================
// NewOutbox — фабрика записи outbox для команд саги
// =============================================================================

// NewOutbox создаёт новую запись outbox для команды саги.
func NewOutbox(id, aggregateID, topic string, cmd *Command, headers map[string]string) (*outboxpkg.Outbox, error) {
	payload, err := cmd.ToJSON()
	if err != nil {
		return nil, err
	}

	return &outboxpkg.Outbox{
		ID:            id,
		AggregateType: "order",
		AggregateID:   aggregateID,
		EventType:     string(cmd.Type),
		Topic:         topic,
		MessageKey:    aggregateID, // Партиционирование по order_id
		Payload:       payload,
		Headers:       headers,
		CreatedAt:     time.Now(),
	}, nil
}
