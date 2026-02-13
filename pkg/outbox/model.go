package outbox

import "time"

// OutboxModel — GORM модель для таблицы outbox.
type OutboxModel struct {
	ID            string     `gorm:"column:id;type:varchar(36);primaryKey"`
	AggregateType string     `gorm:"column:aggregate_type;type:varchar(50);not null;index:idx_outbox_aggregate"`
	AggregateID   string     `gorm:"column:aggregate_id;type:varchar(36);not null;index:idx_outbox_aggregate"`
	EventType     string     `gorm:"column:event_type;type:varchar(100);not null"`
	Topic         string     `gorm:"column:topic;type:varchar(100);not null"`
	MessageKey    string     `gorm:"column:message_key;type:varchar(100);not null"`
	Payload       []byte     `gorm:"column:payload;type:json;not null"`
	Headers       []byte     `gorm:"column:headers;type:json"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime"`
	ProcessedAt   *time.Time `gorm:"column:processed_at;index:idx_outbox_unprocessed"`
	RetryCount    int        `gorm:"column:retry_count;not null;default:0;index:idx_outbox_retry"`
	LastError     *string    `gorm:"column:last_error;type:text"`
}

// TableName возвращает имя таблицы в БД.
func (OutboxModel) TableName() string {
	return "outbox"
}

// ToDomain конвертирует GORM модель в доменную сущность.
func (m *OutboxModel) ToDomain() *Outbox {
	o := &Outbox{
		ID:            m.ID,
		AggregateType: m.AggregateType,
		AggregateID:   m.AggregateID,
		EventType:     m.EventType,
		Topic:         m.Topic,
		MessageKey:    m.MessageKey,
		Payload:       m.Payload,
		CreatedAt:     m.CreatedAt,
		ProcessedAt:   m.ProcessedAt,
		RetryCount:    m.RetryCount,
		LastError:     m.LastError,
	}

	// Десериализуем headers из JSON
	if len(m.Headers) > 0 {
		_ = o.SetHeadersFromJSON(m.Headers)
	}

	return o
}

// ModelFromDomain конвертирует доменную сущность в GORM модель.
func ModelFromDomain(o *Outbox) *OutboxModel {
	model := &OutboxModel{
		ID:            o.ID,
		AggregateType: o.AggregateType,
		AggregateID:   o.AggregateID,
		EventType:     o.EventType,
		Topic:         o.Topic,
		MessageKey:    o.MessageKey,
		Payload:       o.Payload,
		CreatedAt:     o.CreatedAt,
		ProcessedAt:   o.ProcessedAt,
		RetryCount:    o.RetryCount,
		LastError:     o.LastError,
	}

	// Сериализуем headers в JSON
	if o.Headers != nil {
		if data, err := o.HeadersJSON(); err == nil {
			model.Headers = data
		}
	}

	return model
}
