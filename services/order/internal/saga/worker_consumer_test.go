package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"example.com/order-system/pkg/kafka"
)

// Моки определены в mocks_test.go

// =============================================================================
// Тесты ReplyConsumer
// =============================================================================

func TestReplyConsumer_HandleMessage_Success(t *testing.T) {
	ctx := context.Background()
	consumer := new(MockKafkaConsumer)
	orch := new(MockOrchestrator)

	replyConsumer := NewReplyConsumer(consumer, orch)

	// Формируем валидный Reply
	reply := &Reply{
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplySuccess,
		PaymentID: "payment-789",
	}
	replyJSON, err := reply.ToJSON()
	require.NoError(t, err)

	// Мокаем Orchestrator
	orch.On("HandlePaymentReply", mock.Anything, mock.AnythingOfType("*saga.Reply")).
		Run(func(args mock.Arguments) {
			r := args.Get(1).(*Reply)
			assert.Equal(t, "saga-123", r.SagaID)
			assert.Equal(t, "order-456", r.OrderID)
			assert.Equal(t, ReplySuccess, r.Status)
		}).
		Return(nil)

	// Вызываем handleMessage напрямую
	msg := &kafka.Message{
		Topic: kafka.TopicSagaReplies,
		Key:   []byte("order-456"),
		Value: replyJSON,
	}
	err = replyConsumer.handleMessage(ctx, msg)

	require.NoError(t, err)
	orch.AssertExpectations(t)
}

func TestReplyConsumer_HandleMessage_DeserializeError(t *testing.T) {
	ctx := context.Background()
	consumer := new(MockKafkaConsumer)
	orch := new(MockOrchestrator)

	replyConsumer := NewReplyConsumer(consumer, orch)

	// Невалидный JSON
	msg := &kafka.Message{
		Topic: kafka.TopicSagaReplies,
		Key:   []byte("order-456"),
		Value: []byte(`{invalid json}`),
	}
	err := replyConsumer.handleMessage(ctx, msg)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "десериализации")
	// Orchestrator НЕ должен вызываться
	orch.AssertNotCalled(t, "HandlePaymentReply")
}

func TestReplyConsumer_HandleMessage_OrchestratorError(t *testing.T) {
	ctx := context.Background()
	consumer := new(MockKafkaConsumer)
	orch := new(MockOrchestrator)

	replyConsumer := NewReplyConsumer(consumer, orch)

	// Валидный Reply
	reply := &Reply{
		SagaID:  "saga-123",
		OrderID: "order-456",
		Status:  ReplyFailed,
		Error:   "Недостаточно средств",
	}
	replyJSON, _ := reply.ToJSON()

	// Orchestrator возвращает ошибку
	orch.On("HandlePaymentReply", mock.Anything, mock.AnythingOfType("*saga.Reply")).
		Return(errors.New("saga not found"))

	msg := &kafka.Message{
		Topic: kafka.TopicSagaReplies,
		Key:   []byte("order-456"),
		Value: replyJSON,
	}
	err := replyConsumer.handleMessage(ctx, msg)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "обработки ответа")
	orch.AssertExpectations(t)
}

func TestReplyConsumer_Run_ContextCancel(t *testing.T) {
	consumer := new(MockKafkaConsumer)
	orch := new(MockOrchestrator)

	replyConsumer := NewReplyConsumer(consumer, orch)

	ctx, cancel := context.WithCancel(context.Background())

	// ConsumeWithRetry возвращает ошибку context.Canceled
	consumer.On("ConsumeWithRetry", mock.Anything, mock.AnythingOfType("kafka.MessageHandler"), 3).
		Return(context.Canceled)

	// Отменяем context сразу
	cancel()

	err := replyConsumer.Run(ctx)

	assert.Equal(t, context.Canceled, err)
	consumer.AssertExpectations(t)
}

func TestReplyConsumer_Close(t *testing.T) {
	consumer := new(MockKafkaConsumer)
	orch := new(MockOrchestrator)

	replyConsumer := NewReplyConsumer(consumer, orch)

	consumer.On("Close").Return(nil)

	err := replyConsumer.Close()

	require.NoError(t, err)
	consumer.AssertExpectations(t)
}

func TestReplyConsumer_Close_Error(t *testing.T) {
	consumer := new(MockKafkaConsumer)
	orch := new(MockOrchestrator)

	replyConsumer := NewReplyConsumer(consumer, orch)

	consumer.On("Close").Return(errors.New("close error"))

	err := replyConsumer.Close()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close error")
}
