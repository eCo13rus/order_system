package outbox

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

// =============================================================================
// Моки для тестов Outbox Worker
// =============================================================================

// mockOutboxRepository — мок OutboxRepository.
type mockOutboxRepository struct {
	mock.Mock
}

func (m *mockOutboxRepository) Create(ctx context.Context, o *Outbox) error {
	args := m.Called(ctx, o)
	return args.Error(0)
}

func (m *mockOutboxRepository) GetUnprocessed(ctx context.Context, limit int) ([]*Outbox, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*Outbox), args.Error(1)
}

func (m *mockOutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockOutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	args := m.Called(ctx, id, err)
	return args.Error(0)
}

func (m *mockOutboxRepository) DeleteProcessedBefore(ctx context.Context, before time.Time) (int64, error) {
	args := m.Called(ctx, before)
	return args.Get(0).(int64), args.Error(1)
}

// mockKafkaProducer — мок KafkaProducer.
type mockKafkaProducer struct {
	mock.Mock
}

func (m *mockKafkaProducer) SendMessage(ctx context.Context, msg *kafka.Message) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

// =============================================================================
// Тесты OutboxWorker
// =============================================================================

func TestOutboxWorker_ProcessSingle_Success(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(mockOutboxRepository)
	producer := new(mockKafkaProducer)

	worker := NewOutboxWorker(outboxRepo, producer, DefaultWorkerConfig(), "test")

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
	outboxRepo := new(mockOutboxRepository)
	producer := new(mockKafkaProducer)

	worker := NewOutboxWorker(outboxRepo, producer, DefaultWorkerConfig(), "test")

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
	outboxRepo := new(mockOutboxRepository)
	producer := new(mockKafkaProducer)

	cfg := WorkerConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    10,
		MaxRetries:   3,
	}
	worker := NewOutboxWorker(outboxRepo, producer, cfg, "test")

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

	// Вызываем processOutbox напрямую (доступен внутри пакета)
	worker.processOutbox(ctx)

	outboxRepo.AssertExpectations(t)
	// Producer НЕ должен вызываться для dead letter
	producer.AssertNotCalled(t, "SendMessage")
}

func TestOutboxWorker_ProcessOutbox_BatchProcessing(t *testing.T) {
	ctx := context.Background()
	outboxRepo := new(mockOutboxRepository)
	producer := new(mockKafkaProducer)

	cfg := WorkerConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    10,
		MaxRetries:   5,
	}
	worker := NewOutboxWorker(outboxRepo, producer, cfg, "test")

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
	outboxRepo := new(mockOutboxRepository)
	producer := new(mockKafkaProducer)

	worker := NewOutboxWorker(outboxRepo, producer, DefaultWorkerConfig(), "test")

	// Пустой outbox
	outboxRepo.On("GetUnprocessed", ctx, mock.AnythingOfType("int")).Return([]*Outbox{}, nil)

	worker.processOutbox(ctx)

	outboxRepo.AssertExpectations(t)
	// Ничего не должно отправляться
	producer.AssertNotCalled(t, "SendMessage")
}

func TestOutboxWorker_Run_ContextCancel(t *testing.T) {
	outboxRepo := new(mockOutboxRepository)
	producer := new(mockKafkaProducer)

	cfg := WorkerConfig{
		PollInterval: 50 * time.Millisecond,
		BatchSize:    10,
		MaxRetries:   5,
	}
	worker := NewOutboxWorker(outboxRepo, producer, cfg, "test")

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

func TestDefaultWorkerConfig(t *testing.T) {
	cfg := DefaultWorkerConfig()

	// PollInterval = 1s для development (меньше шума в логах)
	assert.Equal(t, 1*time.Second, cfg.PollInterval)
	assert.Equal(t, 100, cfg.BatchSize)
	assert.Equal(t, 5, cfg.MaxRetries)
}
