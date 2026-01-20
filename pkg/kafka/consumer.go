package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"example.com/order-system/pkg/logger"
)

// MessageHandler - функция обработки сообщений.
// Получает context с headers (trace_id, correlation_id) и сообщение.
// Должна вернуть nil при успешной обработке.
type MessageHandler func(ctx context.Context, msg *Message) error

// Consumer читает сообщения из Kafka и передаёт их обработчику.
// Поддерживает graceful shutdown через context.
type Consumer struct {
	reader   *kafka.Reader
	producer *Producer // Для отправки в DLQ
	cfg      Config
	topic    string
}

// NewConsumer создаёт новый Consumer для чтения сообщений из топика.
// groupID используется для consumer group - несколько инстансов с одним groupID
// будут распределять партиции между собой.
func NewConsumer(cfg Config, topic string, groupID string) (*Consumer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("не указаны брокеры Kafka")
	}

	if topic == "" {
		return nil, fmt.Errorf("не указан топик")
	}

	if groupID == "" {
		return nil, fmt.Errorf("не указан group ID")
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        cfg.Brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,    // Минимум 1 байт для быстрой обработки
		MaxBytes:       10e6, // 10MB максимум
		MaxWait:        100 * time.Millisecond,
		CommitInterval: time.Second, // Автокоммит каждую секунду
		StartOffset:    kafka.LastOffset,
	})

	logger.Info().
		Strs("brokers", cfg.Brokers).
		Str("topic", topic).
		Str("group_id", groupID).
		Msg("Создан Kafka Consumer")

	return &Consumer{
		reader: reader,
		cfg:    cfg,
		topic:  topic,
	}, nil
}

// SetDLQProducer устанавливает Producer для отправки ошибочных сообщений в DLQ.
func (c *Consumer) SetDLQProducer(p *Producer) {
	c.producer = p
}

// Consume запускает чтение сообщений из топика.
// Блокирует выполнение до отмены context.
// При отмене context выполняется graceful shutdown.
//
// Пример использования:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	go func() {
//	    <-sigChan
//	    cancel()
//	}()
//	consumer.Consume(ctx, handler)
func (c *Consumer) Consume(ctx context.Context, handler MessageHandler) error {
	logger.Info().
		Str("topic", c.topic).
		Msg("Запуск чтения сообщений из Kafka")

	for {
		// Проверяем отмену context перед чтением.
		select {
		case <-ctx.Done():
			logger.Info().
				Str("topic", c.topic).
				Msg("Получен сигнал завершения, остановка Consumer")
			return ctx.Err()
		default:
		}

		// Читаем сообщение с таймаутом.
		msg, err := c.fetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			logger.Error().
				Err(err).
				Str("topic", c.topic).
				Msg("Ошибка чтения сообщения из Kafka")
			continue
		}

		// Обрабатываем сообщение.
		if err := c.processMessage(ctx, msg, handler); err != nil {
			logger.Error().
				Err(err).
				Str("topic", c.topic).
				Str("key", string(msg.Key)).
				Int("partition", msg.Partition).
				Int64("offset", msg.Offset).
				Msg("Ошибка обработки сообщения")

			// Отправляем в DLQ, если настроен producer.
			if c.producer != nil {
				if dlqErr := c.sendToDLQ(ctx, msg, err); dlqErr != nil {
					logger.Error().
						Err(dlqErr).
						Msg("Ошибка отправки в DLQ")
				}
			}
		}

		// Коммитим offset независимо от результата обработки.
		// Ошибочные сообщения уже в DLQ.
		if err := c.commitMessage(ctx, msg); err != nil {
			logger.Error().
				Err(err).
				Msg("Ошибка коммита offset")
		}
	}
}

// ConsumeWithRetry запускает чтение с автоматическими повторами при ошибках.
// maxRetries - максимальное количество повторов для каждого сообщения.
// При исчерпании повторов сообщение отправляется в DLQ.
func (c *Consumer) ConsumeWithRetry(ctx context.Context, handler MessageHandler, maxRetries int) error {
	retryHandler := func(ctx context.Context, msg *Message) error {
		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				// Экспоненциальная задержка: 100ms, 200ms, 400ms...
				delay := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
				logger.Warn().
					Int("attempt", attempt).
					Str("key", string(msg.Key)).
					Dur("delay", delay).
					Msg("Повторная попытка обработки сообщения")

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
			}

			if err := handler(ctx, msg); err != nil {
				lastErr = err
				continue
			}
			return nil
		}
		return fmt.Errorf("исчерпаны попытки обработки: %w", lastErr)
	}

	return c.Consume(ctx, retryHandler)
}

// fetchMessage читает следующее сообщение из Kafka.
func (c *Consumer) fetchMessage(ctx context.Context) (*Message, error) {
	kafkaMsg, err := c.reader.FetchMessage(ctx)
	if err != nil {
		return nil, err
	}
	return fromKafkaMessage(kafkaMsg), nil
}

// processMessage обрабатывает сообщение, добавляя headers в context.
func (c *Consumer) processMessage(ctx context.Context, msg *Message, handler MessageHandler) error {
	// Создаём context с headers из сообщения.
	msgCtx := c.contextFromMessage(ctx, msg)

	logger.Debug().
		Str("topic", msg.Topic).
		Str("key", string(msg.Key)).
		Int("partition", msg.Partition).
		Int64("offset", msg.Offset).
		Str("trace_id", TraceIDFromContext(msgCtx)).
		Str("correlation_id", CorrelationIDFromContext(msgCtx)).
		Msg("Получено сообщение из Kafka")

	return handler(msgCtx, msg)
}

// contextFromMessage создаёт context с headers из сообщения.
func (c *Consumer) contextFromMessage(ctx context.Context, msg *Message) context.Context {
	// Добавляем trace_id.
	if traceID, ok := msg.Headers[HeaderTraceID]; ok {
		ctx = ContextWithTraceID(ctx, traceID)
	}

	// Добавляем correlation_id.
	if correlationID, ok := msg.Headers[HeaderCorrelationID]; ok {
		ctx = ContextWithCorrelationID(ctx, correlationID)
	}

	return ctx
}

// commitMessage коммитит offset сообщения.
func (c *Consumer) commitMessage(ctx context.Context, msg *Message) error {
	return c.reader.CommitMessages(ctx, kafka.Message{
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
	})
}

// sendToDLQ отправляет сообщение в Dead Letter Queue.
func (c *Consumer) sendToDLQ(ctx context.Context, msg *Message, processingErr error) error {
	logger.Warn().
		Str("topic", msg.Topic).
		Str("key", string(msg.Key)).
		Err(processingErr).
		Msg("Отправка сообщения в DLQ")

	return c.producer.SendToDLQ(ctx, msg, processingErr)
}

// Close закрывает Consumer.
// Должен вызываться при завершении работы приложения.
func (c *Consumer) Close() error {
	logger.Info().
		Str("topic", c.topic).
		Msg("Закрытие Kafka Consumer")

	if err := c.reader.Close(); err != nil {
		logger.Error().
			Err(err).
			Str("topic", c.topic).
			Msg("Ошибка при закрытии Kafka Consumer")
		return fmt.Errorf("ошибка закрытия consumer: %w", err)
	}

	logger.Info().
		Str("topic", c.topic).
		Msg("Kafka Consumer закрыт")
	return nil
}

// Stats возвращает статистику Consumer.
func (c *Consumer) Stats() kafka.ReaderStats {
	return c.reader.Stats()
}

// Lag возвращает текущее отставание Consumer от конца топика.
func (c *Consumer) Lag() int64 {
	return c.reader.Stats().Lag
}
