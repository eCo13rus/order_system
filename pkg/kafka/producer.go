package kafka

import (
	"context"
	"fmt"
	"time"

	"example.com/order-system/pkg/logger"
	"github.com/segmentio/kafka-go"
)

// Producer отправляет сообщения в Kafka с поддержкой headers и трассировки.
type Producer struct {
	writer *kafka.Writer
	cfg    Config
}

// NewProducer создаёт новый Producer для отправки сообщений в Kafka.
// Поддерживает как sync, так и async режимы отправки.
func NewProducer(cfg Config) (*Producer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("не указаны брокеры Kafka")
	}

	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond, // Быстрая отправка для саги
		RequiredAcks: kafka.RequireOne,      // Ждём подтверждения от лидера
		Async:        false,                 // По умолчанию sync режим
	}

	logger.Info().
		Strs("brokers", cfg.Brokers).
		Msg("Создан Kafka Producer")

	return &Producer{
		writer: writer,
		cfg:    cfg,
	}, nil
}

// Send отправляет сообщение в указанный топик.
// Автоматически добавляет headers: trace_id, correlation_id, timestamp.
// Headers извлекаются из context, если они там есть.
func (p *Producer) Send(ctx context.Context, topic string, key []byte, value []byte) error {
	return p.SendWithHeaders(ctx, topic, key, value, nil)
}

// SendWithHeaders отправляет сообщение с дополнительными headers.
// Стандартные headers (trace_id, correlation_id, timestamp) добавляются автоматически.
func (p *Producer) SendWithHeaders(ctx context.Context, topic string, key []byte, value []byte, extraHeaders map[string]string) error {
	// Собираем headers из context и дополнительных.
	headers := p.buildHeaders(ctx, extraHeaders)

	msg := kafka.Message{
		Topic:   topic,
		Key:     key,
		Value:   value,
		Headers: headers,
		Time:    time.Now(),
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		logger.Error().
			Err(err).
			Str("topic", topic).
			Str("key", string(key)).
			Str("trace_id", TraceIDFromContext(ctx)).
			Msg("Ошибка отправки сообщения в Kafka")
		return fmt.Errorf("ошибка отправки в Kafka: %w", err)
	}

	logger.Debug().
		Str("topic", topic).
		Str("key", string(key)).
		Str("trace_id", TraceIDFromContext(ctx)).
		Str("correlation_id", CorrelationIDFromContext(ctx)).
		Msg("Сообщение отправлено в Kafka")

	return nil
}

// SendMessage отправляет подготовленный Message.
func (p *Producer) SendMessage(ctx context.Context, msg *Message) error {
	// Добавляем стандартные headers, если их нет.
	if msg.Headers == nil {
		msg.Headers = make(map[string]string)
	}

	// Добавляем trace_id из context, если не задан.
	if _, ok := msg.Headers[HeaderTraceID]; !ok {
		if traceID := TraceIDFromContext(ctx); traceID != "" {
			msg.Headers[HeaderTraceID] = traceID
		}
	}

	// Добавляем correlation_id из context, если не задан.
	if _, ok := msg.Headers[HeaderCorrelationID]; !ok {
		if correlationID := CorrelationIDFromContext(ctx); correlationID != "" {
			msg.Headers[HeaderCorrelationID] = correlationID
		}
	}

	// Добавляем timestamp.
	if _, ok := msg.Headers[HeaderTimestamp]; !ok {
		msg.Headers[HeaderTimestamp] = time.Now().UTC().Format(time.RFC3339Nano)
	}

	kafkaMsg := msg.toKafkaMessage()
	if err := p.writer.WriteMessages(ctx, kafkaMsg); err != nil {
		logger.Error().
			Err(err).
			Str("topic", msg.Topic).
			Str("key", string(msg.Key)).
			Msg("Ошибка отправки сообщения в Kafka")
		return fmt.Errorf("ошибка отправки в Kafka: %w", err)
	}

	logger.Debug().
		Str("topic", msg.Topic).
		Str("key", string(msg.Key)).
		Msg("Сообщение отправлено в Kafka")

	return nil
}

// SendToDLQ отправляет сообщение в Dead Letter Queue с информацией об ошибке.
func (p *Producer) SendToDLQ(ctx context.Context, originalMsg *Message, processingError error) error {
	dlqHeaders := make(map[string]string)
	for k, v := range originalMsg.Headers {
		dlqHeaders[k] = v
	}

	// Добавляем информацию об ошибке.
	dlqHeaders["dlq_error"] = processingError.Error()
	dlqHeaders["dlq_original_topic"] = originalMsg.Topic
	dlqHeaders["dlq_timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)

	return p.SendWithHeaders(ctx, TopicDLQ, originalMsg.Key, originalMsg.Value, dlqHeaders)
}

// buildHeaders собирает headers из context и дополнительных параметров.
func (p *Producer) buildHeaders(ctx context.Context, extra map[string]string) []kafka.Header {
	headers := make([]kafka.Header, 0, 3+len(extra))

	// Добавляем trace_id из context.
	if traceID := TraceIDFromContext(ctx); traceID != "" {
		headers = append(headers, kafka.Header{
			Key:   HeaderTraceID,
			Value: []byte(traceID),
		})
	}

	// Добавляем correlation_id из context.
	if correlationID := CorrelationIDFromContext(ctx); correlationID != "" {
		headers = append(headers, kafka.Header{
			Key:   HeaderCorrelationID,
			Value: []byte(correlationID),
		})
	}

	// Добавляем timestamp.
	headers = append(headers, kafka.Header{
		Key:   HeaderTimestamp,
		Value: []byte(time.Now().UTC().Format(time.RFC3339Nano)),
	})

	// Добавляем дополнительные headers.
	for k, v := range extra {
		headers = append(headers, kafka.Header{
			Key:   k,
			Value: []byte(v),
		})
	}

	return headers
}

// Close закрывает соединение с Kafka.
// Должен вызываться при завершении работы приложения.
func (p *Producer) Close() error {
	logger.Info().Msg("Закрытие Kafka Producer")

	if err := p.writer.Close(); err != nil {
		logger.Error().Err(err).Msg("Ошибка при закрытии Kafka Producer")
		return fmt.Errorf("ошибка закрытия producer: %w", err)
	}

	logger.Info().Msg("Kafka Producer закрыт")
	return nil
}
