package saga

import (
	"context"
	"fmt"

	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
)

// =============================================================================
// ReplyConsumer — обработчик ответов от Payment Service
// =============================================================================

// KafkaConsumer — интерфейс для чтения сообщений из Kafka.
// Позволяет замокать kafka.Consumer в unit-тестах (Dependency Inversion).
type KafkaConsumer interface {
	ConsumeWithRetry(ctx context.Context, handler kafka.MessageHandler, maxRetries int) error
	Close() error
}

// ReplyConsumer слушает топик saga.replies и обрабатывает ответы от Payment Service.
// При получении ответа делегирует обработку в Orchestrator.
type ReplyConsumer struct {
	consumer     KafkaConsumer // Интерфейс для тестируемости
	orchestrator Orchestrator
}

// NewReplyConsumer создаёт новый consumer для ответов саги.
// consumer — интерфейс KafkaConsumer (обычно *kafka.Consumer, но можно замокать).
func NewReplyConsumer(consumer KafkaConsumer, orchestrator Orchestrator) *ReplyConsumer {
	return &ReplyConsumer{
		consumer:     consumer,
		orchestrator: orchestrator,
	}
}

// Run запускает чтение ответов из Kafka. Блокирует до отмены контекста.
//
// Пример использования:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	go replyConsumer.Run(ctx)
//	// ...
//	cancel() // Остановка
func (c *ReplyConsumer) Run(ctx context.Context) error {
	log := logger.FromContext(ctx)
	log.Info().
		Str("topic", kafka.TopicSagaReplies).
		Msg("Запуск Reply Consumer")

	// Запускаем consumer с retry
	return c.consumer.ConsumeWithRetry(ctx, c.handleMessage, 3)
}

// handleMessage обрабатывает одно сообщение из Kafka.
func (c *ReplyConsumer) handleMessage(ctx context.Context, msg *kafka.Message) error {
	log := logger.FromContext(ctx)

	log.Debug().
		Str("topic", msg.Topic).
		Str("key", string(msg.Key)).
		Msg("Получен ответ от Payment Service")

	// Десериализуем ответ
	reply, err := ReplyFromJSON(msg.Value)
	if err != nil {
		log.Error().
			Err(err).
			Str("payload", string(msg.Value)).
			Msg("Ошибка десериализации ответа")
		return fmt.Errorf("ошибка десериализации: %w", err)
	}

	// Делегируем обработку в Orchestrator
	if err := c.orchestrator.HandlePaymentReply(ctx, reply); err != nil {
		log.Error().
			Err(err).
			Str("saga_id", reply.SagaID).
			Str("order_id", reply.OrderID).
			Msg("Ошибка обработки ответа")
		return fmt.Errorf("ошибка обработки ответа: %w", err)
	}

	log.Info().
		Str("saga_id", reply.SagaID).
		Str("order_id", reply.OrderID).
		Str("status", string(reply.Status)).
		Msg("Ответ от Payment Service обработан")

	return nil
}

// Close закрывает consumer.
func (c *ReplyConsumer) Close() error {
	return c.consumer.Close()
}
