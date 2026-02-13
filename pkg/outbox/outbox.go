// Package outbox реализует Outbox Pattern для гарантированной доставки сообщений в Kafka.
// Используется Order Service (saga.commands) и Payment Service (saga.replies).
// В одной транзакции пишем бизнес-данные + запись в outbox.
// Отдельный OutboxWorker читает outbox и отправляет в Kafka.
package outbox

import (
	"encoding/json"
	"time"
)

// Outbox — запись в таблице outbox для гарантированной доставки в Kafka.
type Outbox struct {
	ID            string            // UUID записи
	AggregateType string            // Тип агрегата (order / payment)
	AggregateID   string            // ID агрегата (order_id)
	EventType     string            // Тип события (saga.command.process_payment / saga.reply.SUCCESS)
	Topic         string            // Kafka топик
	MessageKey    string            // Ключ сообщения (для партиционирования)
	Payload       []byte            // JSON payload
	Headers       map[string]string // Headers для Kafka (trace_id, correlation_id)
	CreatedAt     time.Time         // Время создания
	ProcessedAt   *time.Time        // Время обработки (nil = не обработана)
	RetryCount    int               // Количество попыток отправки
	LastError     *string           // Последняя ошибка
}

// HeadersJSON возвращает headers в формате JSON для БД.
func (o *Outbox) HeadersJSON() ([]byte, error) {
	if o.Headers == nil {
		return nil, nil
	}
	return json.Marshal(o.Headers)
}

// SetHeadersFromJSON устанавливает headers из JSON.
func (o *Outbox) SetHeadersFromJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &o.Headers)
}
