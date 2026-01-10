package saga

import (
	"encoding/json"
	"time"
)

// =============================================================================
// Outbox — запись для Outbox Pattern
// =============================================================================

// Outbox — запись в таблице outbox для гарантированной доставки в Kafka.
// Outbox Pattern: в одной транзакции пишем бизнес-данные + событие в outbox.
// Отдельный worker читает outbox и отправляет в Kafka.
type Outbox struct {
	ID            string            // UUID записи
	AggregateType string            // Тип агрегата (order)
	AggregateID   string            // ID агрегата (order_id)
	EventType     string            // Тип события (saga.command.process_payment)
	Topic         string            // Kafka топик
	MessageKey    string            // Ключ сообщения (для партиционирования)
	Payload       []byte            // JSON payload
	Headers       map[string]string // Headers для Kafka
	CreatedAt     time.Time         // Время создания
	ProcessedAt   *time.Time        // Время обработки (nil = не обработана)
	RetryCount    int               // Количество попыток отправки
	LastError     *string           // Последняя ошибка
}

// NewOutbox создаёт новую запись outbox для команды саги.
func NewOutbox(id, aggregateID, topic string, cmd *Command, headers map[string]string) (*Outbox, error) {
	payload, err := cmd.ToJSON()
	if err != nil {
		return nil, err
	}

	return &Outbox{
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

// HeadersJSON возвращает headers в формате JSON для БД.
func (o *Outbox) HeadersJSON() ([]byte, error) {
	if o.Headers == nil {
		return nil, nil
	}
	return json.Marshal(o.Headers)
}

// SetHeadersFromJSON устанавливает headers из JSON.
func (o *Outbox) SetHeadersFromJSON(data []byte) error {
	// len() для nil slice возвращает 0, поэтому отдельная проверка на nil избыточна
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &o.Headers)
}
