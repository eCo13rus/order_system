package outbox

import (
	"context"
	"time"

	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
)

// KafkaProducer — интерфейс для отправки сообщений в Kafka.
// Позволяет замокать kafka.Producer в unit-тестах (Dependency Inversion).
type KafkaProducer interface {
	SendMessage(ctx context.Context, msg *kafka.Message) error
}

// WorkerConfig — настройки Outbox Worker.
type WorkerConfig struct {
	// PollInterval — интервал между опросами таблицы outbox.
	PollInterval time.Duration

	// BatchSize — количество записей за один запрос.
	BatchSize int

	// MaxRetries — максимальное количество попыток отправки.
	// После превышения запись помечается как "dead letter".
	MaxRetries int
}

// DefaultWorkerConfig возвращает конфигурацию по умолчанию.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval: 1 * time.Second,
		BatchSize:    100,
		MaxRetries:   5,
	}
}

// OutboxWorker читает записи из outbox и отправляет их в Kafka.
// Реализует гарантию "at-least-once" доставки.
type OutboxWorker struct {
	repo     OutboxRepository
	producer KafkaProducer
	cfg      WorkerConfig
	name     string // Имя для идентификации в логах (order / payment)
}

// NewOutboxWorker создаёт новый Outbox Worker.
// name — имя сервиса для логов (например, "order" или "payment").
func NewOutboxWorker(repo OutboxRepository, producer KafkaProducer, cfg WorkerConfig, name string) *OutboxWorker {
	return &OutboxWorker{
		repo:     repo,
		producer: producer,
		cfg:      cfg,
		name:     name,
	}
}

// cleanupInterval — интервал очистки обработанных записей outbox (1 час).
const cleanupInterval = 1 * time.Hour

// cleanupRetention — срок хранения обработанных записей outbox (7 дней).
const cleanupRetention = 7 * 24 * time.Hour

// Run запускает Worker. Блокирует выполнение до отмены контекста.
func (w *OutboxWorker) Run(ctx context.Context) {
	log := logger.FromContext(ctx)
	log.Info().
		Str("name", w.name).
		Dur("poll_interval", w.cfg.PollInterval).
		Int("batch_size", w.cfg.BatchSize).
		Msg("Запуск Outbox Worker")

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Str("name", w.name).Msg("Остановка Outbox Worker")
			return
		case <-ticker.C:
			w.processOutbox(ctx)
		case <-cleanupTicker.C:
			w.cleanupProcessed(ctx)
		}
	}
}

// cleanupProcessed удаляет обработанные записи outbox старше 7 дней.
func (w *OutboxWorker) cleanupProcessed(ctx context.Context) {
	log := logger.FromContext(ctx)

	before := time.Now().Add(-cleanupRetention)
	deleted, err := w.repo.DeleteProcessedBefore(ctx, before)
	if err != nil {
		log.Error().Err(err).Str("name", w.name).Msg("Ошибка очистки outbox")
		return
	}

	if deleted > 0 {
		log.Info().Int64("deleted", deleted).Str("name", w.name).Msg("Очистка обработанных записей outbox")
	}
}

// processOutbox обрабатывает пачку необработанных записей.
func (w *OutboxWorker) processOutbox(ctx context.Context) {
	log := logger.FromContext(ctx)

	// Получаем необработанные записи
	records, err := w.repo.GetUnprocessed(ctx, w.cfg.BatchSize)
	if err != nil {
		log.Error().Err(err).Str("name", w.name).Msg("Ошибка чтения outbox")
		return
	}

	if len(records) == 0 {
		return
	}

	log.Debug().Int("count", len(records)).Str("name", w.name).Msg("Обработка записей outbox")

	for _, record := range records {
		// Проверяем контекст перед обработкой
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Dead letter: записи с превышенным retry_count помечаем как processed
		if record.RetryCount >= w.cfg.MaxRetries {
			log.Warn().
				Str("outbox_id", record.ID).
				Str("event_type", record.EventType).
				Str("aggregate_id", record.AggregateID).
				Int("retry_count", record.RetryCount).
				Msg("Dead letter: превышен лимит попыток, запись выведена из очереди")

			if err := w.repo.MarkProcessed(ctx, record.ID); err != nil {
				log.Error().Err(err).Str("outbox_id", record.ID).Msg("Ошибка пометки dead letter")
			}
			continue
		}

		w.sendToKafka(ctx, record)
	}
}

// sendToKafka отправляет запись в Kafka.
func (w *OutboxWorker) sendToKafka(ctx context.Context, record *Outbox) {
	log := logger.FromContext(ctx)

	// Формируем сообщение для Kafka
	msg := &kafka.Message{
		Topic:   record.Topic,
		Key:     []byte(record.MessageKey),
		Value:   record.Payload,
		Headers: record.Headers,
	}

	// Отправляем в Kafka
	if err := w.producer.SendMessage(ctx, msg); err != nil {
		log.Error().
			Err(err).
			Str("outbox_id", record.ID).
			Str("topic", record.Topic).
			Msg("Ошибка отправки в Kafka")

		if markErr := w.repo.MarkFailed(ctx, record.ID, err); markErr != nil {
			log.Error().Err(markErr).Str("outbox_id", record.ID).Msg("Ошибка пометки outbox как failed")
		}
		return
	}

	// Помечаем как обработанную
	if err := w.repo.MarkProcessed(ctx, record.ID); err != nil {
		log.Error().
			Err(err).
			Str("outbox_id", record.ID).
			Msg("Ошибка пометки outbox как обработанной")
		return
	}

	log.Debug().
		Str("outbox_id", record.ID).
		Str("topic", record.Topic).
		Str("event_type", record.EventType).
		Msg("Сообщение отправлено в Kafka")
}

// ProcessSingle обрабатывает одну запись outbox (для тестирования).
func (w *OutboxWorker) ProcessSingle(ctx context.Context, record *Outbox) error {
	msg := &kafka.Message{
		Topic:   record.Topic,
		Key:     []byte(record.MessageKey),
		Value:   record.Payload,
		Headers: record.Headers,
	}

	if err := w.producer.SendMessage(ctx, msg); err != nil {
		_ = w.repo.MarkFailed(ctx, record.ID, err)
		return err
	}

	return w.repo.MarkProcessed(ctx, record.ID)
}
