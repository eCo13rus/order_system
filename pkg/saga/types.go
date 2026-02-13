// Package saga содержит общие типы команд и ответов для Saga Orchestration.
// Используется Order Service (отправитель команд) и Payment Service (обработчик команд).
// Единый источник правды для Command/Reply — исключает рассинхронизацию типов между сервисами.
package saga

import (
	"encoding/json"
	"time"
)

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

// Command — команда, отправляемая из Order Service в Payment Service через Kafka.
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
