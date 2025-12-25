// Package kafka предоставляет обёртки над kafka-go для реализации Saga Pattern.
// Включает Producer и Consumer с поддержкой headers, трассировки и graceful shutdown.
package kafka

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
)

// Топики для Saga Pattern.
const (
	// TopicSagaCommands - топик для команд саги (Order Service -> Payment Service).
	TopicSagaCommands = "saga.commands"

	// TopicSagaReplies - топик для ответов на команды (Payment Service -> Order Service).
	TopicSagaReplies = "saga.replies"

	// TopicDLQ - Dead Letter Queue для необработанных сообщений.
	TopicDLQ = "dlq.saga"
)

// Ключи для headers сообщений.
const (
	// HeaderTraceID - идентификатор трассировки для distributed tracing.
	HeaderTraceID = "trace_id"

	// HeaderCorrelationID - идентификатор корреляции для связи запросов и ответов.
	HeaderCorrelationID = "correlation_id"

	// HeaderTimestamp - временная метка создания сообщения.
	HeaderTimestamp = "timestamp"
)

// Ключи для context.
type contextKey string

const (
	// ContextKeyTraceID - ключ для trace_id в context.
	ContextKeyTraceID contextKey = "trace_id"

	// ContextKeyCorrelationID - ключ для correlation_id в context.
	ContextKeyCorrelationID contextKey = "correlation_id"
)

// Config содержит настройки для подключения к Kafka.
type Config struct {
	// Brokers - список адресов брокеров Kafka.
	Brokers []string

	// ConsumerGroup - имя consumer group для Consumer.
	ConsumerGroup string
}

// Message представляет сообщение Kafka с метаданными.
type Message struct {
	// Key - ключ сообщения для партиционирования.
	Key []byte

	// Value - тело сообщения (payload).
	Value []byte

	// Topic - топик сообщения.
	Topic string

	// Partition - номер партиции.
	Partition int

	// Offset - смещение сообщения в партиции.
	Offset int64

	// Headers - заголовки сообщения (trace_id, correlation_id и т.д.).
	Headers map[string]string

	// Time - временная метка сообщения.
	Time time.Time
}

// fromKafkaMessage конвертирует kafka.Message в Message.
func fromKafkaMessage(m kafka.Message) *Message {
	headers := make(map[string]string, len(m.Headers))
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}

	return &Message{
		Key:       m.Key,
		Value:     m.Value,
		Topic:     m.Topic,
		Partition: m.Partition,
		Offset:    m.Offset,
		Headers:   headers,
		Time:      m.Time,
	}
}

// toKafkaMessage конвертирует Message в kafka.Message.
func (m *Message) toKafkaMessage() kafka.Message {
	headers := make([]kafka.Header, 0, len(m.Headers))
	for k, v := range m.Headers {
		headers = append(headers, kafka.Header{
			Key:   k,
			Value: []byte(v),
		})
	}

	return kafka.Message{
		Key:     m.Key,
		Value:   m.Value,
		Topic:   m.Topic,
		Headers: headers,
		Time:    m.Time,
	}
}

// TraceIDFromContext извлекает trace_id из context.
func TraceIDFromContext(ctx context.Context) string {
	if v := ctx.Value(ContextKeyTraceID); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// CorrelationIDFromContext извлекает correlation_id из context.
func CorrelationIDFromContext(ctx context.Context) string {
	if v := ctx.Value(ContextKeyCorrelationID); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ContextWithTraceID добавляет trace_id в context.
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ContextKeyTraceID, traceID)
}

// ContextWithCorrelationID добавляет correlation_id в context.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, ContextKeyCorrelationID, correlationID)
}
