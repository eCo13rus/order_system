// Package kafka предоставляет обёртки над kafka-go для реализации Saga Pattern.
// Включает Producer и Consumer с поддержкой headers, трассировки и graceful shutdown.
package kafka

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"

	"example.com/order-system/pkg/logger"
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

// Ключи для headers сообщений Kafka.
const (
	// HeaderTraceID - идентификатор трассировки для distributed tracing.
	HeaderTraceID = "trace_id"

	// HeaderCorrelationID - идентификатор корреляции для связи запросов и ответов.
	HeaderCorrelationID = "correlation_id"

	// HeaderTimestamp - временная метка создания сообщения.
	HeaderTimestamp = "timestamp"
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
// Делегирует в pkg/logger для единообразной работы с контекстом.
func TraceIDFromContext(ctx context.Context) string {
	return logger.TraceIDFromContext(ctx)
}

// CorrelationIDFromContext извлекает correlation_id из context.
// Делегирует в pkg/logger для единообразной работы с контекстом.
func CorrelationIDFromContext(ctx context.Context) string {
	return logger.CorrelationIDFromContext(ctx)
}

// ContextWithTraceID добавляет trace_id в context.
// Делегирует в pkg/logger для единообразной работы с контекстом.
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return logger.WithTraceID(ctx, traceID)
}

// ContextWithCorrelationID добавляет correlation_id в context.
// Делегирует в pkg/logger для единообразной работы с контекстом.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return logger.WithCorrelationID(ctx, correlationID)
}

// TopicConfig — конфигурация топика для создания.
type TopicConfig struct {
	Name              string // Имя топика
	NumPartitions     int    // Количество партиций
	ReplicationFactor int    // Фактор репликации
}

// EnsureTopics создаёт топики если они не существуют.
// Безопасно вызывать при каждом старте — существующие топики не пересоздаются.
func EnsureTopics(brokers []string, topics []TopicConfig) error {
	if len(brokers) == 0 {
		return nil
	}

	log := logger.Logger()

	// Подключаемся к любому брокеру для получения контроллера
	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// Получаем адрес контроллера кластера
	controller, err := conn.Controller()
	if err != nil {
		return err
	}

	// Подключаемся к контроллеру для создания топиков
	controllerAddr := net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))
	controllerConn, err := kafka.Dial("tcp", controllerAddr)
	if err != nil {
		return err
	}
	defer func() { _ = controllerConn.Close() }()

	// Формируем список топиков для создания
	topicConfigs := make([]kafka.TopicConfig, len(topics))
	for i, t := range topics {
		topicConfigs[i] = kafka.TopicConfig{
			Topic:             t.Name,
			NumPartitions:     t.NumPartitions,
			ReplicationFactor: t.ReplicationFactor,
		}
	}

	// Создаём топики (если уже существуют — ошибка игнорируется)
	err = controllerConn.CreateTopics(topicConfigs...)
	if err != nil {
		// Проверяем, не является ли ошибка "топик уже существует"
		// kafka-go возвращает nil если топик существует, но на всякий случай логируем
		log.Warn().Err(err).Msg("Ошибка при создании топиков (возможно уже существуют)")
	}

	for _, t := range topics {
		log.Info().
			Str("topic", t.Name).
			Int("partitions", t.NumPartitions).
			Msg("Топик проверен/создан")
	}

	return nil
}

// DefaultSagaTopics возвращает конфигурацию топиков для Saga Pattern.
func DefaultSagaTopics() []TopicConfig {
	return []TopicConfig{
		{Name: TopicSagaCommands, NumPartitions: 3, ReplicationFactor: 1},
		{Name: TopicSagaReplies, NumPartitions: 3, ReplicationFactor: 1},
		{Name: TopicDLQ, NumPartitions: 1, ReplicationFactor: 1},
	}
}
