package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"example.com/order-system/pkg/kafka"
)

// Моки определены в mocks_test.go

// =============================================================================
// Тесты OutboxWorker
// =============================================================================

func TestOutboxWorker_ProcessSingle_Success(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(MockOutboxRepository)
	producer := new(MockKafkaProducer)

	worker := NewOutboxWorker(outboxRepo, producer, DefaultWorkerConfig())

	record := &Outbox{
		ID:         "outbox-123",
		Topic:      "saga.commands",
		MessageKey: "order-456",
		Payload:    []byte(`{"type":"ProcessPayment"}`),
		Headers:    map[string]string{"trace_id": "trace-789"},
	}

	// Ожидаем успешную отправку
	producer.On("SendMessage", ctx, mock.AnythingOfType("*kafka.Message")).Return(nil)
	outboxRepo.On("MarkProcessed", ctx, "outbox-123").Return(nil)

	err := worker.ProcessSingle(ctx, record)

	require.NoError(t, err)
	producer.AssertExpectations(t)
	outboxRepo.AssertExpectations(t)
}

func TestOutboxWorker_ProcessSingle_SendError(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(MockOutboxRepository)
	producer := new(MockKafkaProducer)

	worker := NewOutboxWorker(outboxRepo, producer, DefaultWorkerConfig())

	record := &Outbox{
		ID:         "outbox-123",
		Topic:      "saga.commands",
		MessageKey: "order-456",
		Payload:    []byte(`{"type":"ProcessPayment"}`),
	}

	// Ошибка отправки в Kafka
	sendErr := errors.New("kafka unavailable")
	producer.On("SendMessage", ctx, mock.AnythingOfType("*kafka.Message")).Return(sendErr)
	outboxRepo.On("MarkFailed", ctx, "outbox-123", sendErr).Return(nil)

	err := worker.ProcessSingle(ctx, record)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kafka unavailable")
	producer.AssertExpectations(t)
	outboxRepo.AssertExpectations(t)
	// MarkProcessed НЕ должен вызываться
	outboxRepo.AssertNotCalled(t, "MarkProcessed")
}

func TestOutboxWorker_ProcessOutbox_DeadLetter(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(MockOutboxRepository)
	producer := new(MockKafkaProducer)

	cfg := WorkerConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    10,
		MaxRetries:   3,
	}
	worker := NewOutboxWorker(outboxRepo, producer, cfg)

	// Запись с превышенным retry_count — dead letter
	deadLetter := &Outbox{
		ID:          "outbox-dead",
		Topic:       "saga.commands",
		MessageKey:  "order-789",
		EventType:   "ProcessPayment",
		AggregateID: "order-789",
		Payload:     []byte(`{}`),
		RetryCount:  5, // >= MaxRetries (3)
	}

	// GetUnprocessed возвращает dead letter
	outboxRepo.On("GetUnprocessed", ctx, cfg.BatchSize).Return([]*Outbox{deadLetter}, nil)
	// Ожидаем, что dead letter будет помечен как processed
	outboxRepo.On("MarkProcessed", ctx, "outbox-dead").Return(nil)

	// Вызываем processOutbox напрямую
	worker.processOutbox(ctx)

	outboxRepo.AssertExpectations(t)
	// Producer НЕ должен вызываться для dead letter
	producer.AssertNotCalled(t, "SendMessage")
}

func TestOutboxWorker_ProcessOutbox_BatchProcessing(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(MockOutboxRepository)
	producer := new(MockKafkaProducer)

	cfg := WorkerConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    10,
		MaxRetries:   5,
	}
	worker := NewOutboxWorker(outboxRepo, producer, cfg)

	// Две записи для обработки
	records := []*Outbox{
		{ID: "outbox-1", Topic: "saga.commands", MessageKey: "order-1", Payload: []byte(`{}`)},
		{ID: "outbox-2", Topic: "saga.commands", MessageKey: "order-2", Payload: []byte(`{}`)},
	}

	outboxRepo.On("GetUnprocessed", ctx, cfg.BatchSize).Return(records, nil)
	producer.On("SendMessage", ctx, mock.AnythingOfType("*kafka.Message")).Return(nil).Times(2)
	outboxRepo.On("MarkProcessed", ctx, "outbox-1").Return(nil)
	outboxRepo.On("MarkProcessed", ctx, "outbox-2").Return(nil)

	worker.processOutbox(ctx)

	outboxRepo.AssertExpectations(t)
	producer.AssertExpectations(t)
}

func TestOutboxWorker_ProcessOutbox_Empty(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(MockOutboxRepository)
	producer := new(MockKafkaProducer)

	worker := NewOutboxWorker(outboxRepo, producer, DefaultWorkerConfig())

	// Пустой outbox
	outboxRepo.On("GetUnprocessed", ctx, mock.AnythingOfType("int")).Return([]*Outbox{}, nil)

	worker.processOutbox(ctx)

	outboxRepo.AssertExpectations(t)
	// Ничего не должно отправляться
	producer.AssertNotCalled(t, "SendMessage")
}

func TestOutboxWorker_Run_ContextCancel(t *testing.T) {
	outboxRepo := new(MockOutboxRepository)
	producer := new(MockKafkaProducer)

	cfg := WorkerConfig{
		PollInterval: 50 * time.Millisecond,
		BatchSize:    10,
		MaxRetries:   5,
	}
	worker := NewOutboxWorker(outboxRepo, producer, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Возвращаем пустой список
	outboxRepo.On("GetUnprocessed", mock.Anything, cfg.BatchSize).Return([]*Outbox{}, nil)

	// Запускаем worker в горутине
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	// Даём worker поработать немного
	time.Sleep(100 * time.Millisecond)

	// Отменяем context
	cancel()

	// Проверяем graceful shutdown
	select {
	case <-done:
		// OK — worker остановился
	case <-time.After(time.Second):
		t.Fatal("Worker не остановился после отмены context")
	}
}

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
		Timestamp: time.Now(),
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
		SagaID:    "saga-123",
		OrderID:   "order-456",
		Status:    ReplyFailed,
		Error:     "Недостаточно средств",
		Timestamp: time.Now(),
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

// =============================================================================
// Тесты конфигурации
// =============================================================================

func TestDefaultWorkerConfig(t *testing.T) {
	cfg := DefaultWorkerConfig()

	// PollInterval = 1s для development (меньше шума в логах)
	assert.Equal(t, 1*time.Second, cfg.PollInterval)
	assert.Equal(t, 100, cfg.BatchSize)
	assert.Equal(t, 5, cfg.MaxRetries)
}
