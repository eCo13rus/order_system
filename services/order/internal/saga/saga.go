// Package saga реализует Saga Orchestration для распределённых транзакций.
// Order Service выступает координатором саги, управляя шагами:
// 1. Создание заказа (PENDING)
// 2. Запрос оплаты → Payment Service
// 3. Подтверждение или откат заказа
package saga

import (
	"encoding/json"
	"errors"
	"time"
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
// Команды Saga (Order Service → Payment Service)
// =============================================================================

// CommandType — тип команды саги.
type CommandType string

const (
	// CommandProcessPayment — команда на обработку платежа.
	CommandProcessPayment CommandType = "PROCESS_PAYMENT"

	// CommandRefundPayment — команда на возврат платежа (компенсация).
	CommandRefundPayment CommandType = "REFUND_PAYMENT"
)

// Command — команда, отправляемая в Payment Service через Kafka.
type Command struct {
	SagaID    string      `json:"saga_id"`   // ID саги для корреляции ответа
	OrderID   string      `json:"order_id"`  // ID заказа
	UserID    string      `json:"user_id"`   // ID пользователя
	Type      CommandType `json:"type"`      // Тип команды
	Amount    int64       `json:"amount"`    // Сумма в минимальных единицах
	Currency  string      `json:"currency"`  // Валюта
	Timestamp time.Time   `json:"timestamp"` // Время создания команды
}

// ToJSON сериализует команду в JSON.
func (c *Command) ToJSON() ([]byte, error) {
	return json.Marshal(c)
}

// CommandFromJSON десериализует команду из JSON.
func CommandFromJSON(data []byte) (*Command, error) {
	var cmd Command
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, err
	}
	return &cmd, nil
}

// =============================================================================
// Ответы Saga (Payment Service → Order Service)
// =============================================================================

// ReplyStatus — статус ответа от Payment Service.
type ReplyStatus string

const (
	// ReplySuccess — операция выполнена успешно.
	ReplySuccess ReplyStatus = "SUCCESS"

	// ReplyFailed — операция завершилась с ошибкой.
	ReplyFailed ReplyStatus = "FAILED"
)

// Reply — ответ от Payment Service через Kafka.
type Reply struct {
	SagaID    string      `json:"saga_id"`              // ID саги для корреляции
	OrderID   string      `json:"order_id"`             // ID заказа
	Status    ReplyStatus `json:"status"`               // Результат операции
	PaymentID string      `json:"payment_id,omitempty"` // ID платежа (при успехе)
	Error     string      `json:"error,omitempty"`      // Текст ошибки (при неудаче)
	Timestamp time.Time   `json:"timestamp"`            // Время ответа
}

// ToJSON сериализует ответ в JSON.
func (r *Reply) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// ReplyFromJSON десериализует ответ из JSON.
func ReplyFromJSON(data []byte) (*Reply, error) {
	var reply Reply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// IsSuccess возвращает true, если операция успешна.
func (r *Reply) IsSuccess() bool {
	return r.Status == ReplySuccess
}
