package saga

import (
	"context"
	"time"

	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
)

// =============================================================================
// OutboxWorker — воркер для отправки сообщений из outbox в Kafka
// =============================================================================

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
	// После превышения запись помечается как "dead letter" и выводится из очереди.
	MaxRetries int
}

// DefaultWorkerConfig возвращает конфигурацию по умолчанию.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval: 1 * time.Second, // 1 секунда для development (меньше шума в логах)
		BatchSize:    100,
		MaxRetries:   5,
	}
}

// OutboxWorker читает записи из outbox и отправляет их в Kafka.
// Реализует гарантию "at-least-once" доставки.
type OutboxWorker struct {
	repo     OutboxRepository
	producer KafkaProducer // Интерфейс для тестируемости
	cfg      WorkerConfig
}

// NewOutboxWorker создаёт новый Outbox Worker.
// producer — интерфейс KafkaProducer (обычно *kafka.Producer, но можно замокать).
func NewOutboxWorker(repo OutboxRepository, producer KafkaProducer, cfg WorkerConfig) *OutboxWorker {
	return &OutboxWorker{
		repo:     repo,
		producer: producer,
		cfg:      cfg,
	}
}

// Run запускает Worker. Блокирует выполнение до отмены контекста.
// Реализует graceful shutdown через context.
//
// Пример использования:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	go worker.Run(ctx)
//	// ...
//	cancel() // Остановка worker
func (w *OutboxWorker) Run(ctx context.Context) {
	log := logger.FromContext(ctx)
	log.Info().
		Dur("poll_interval", w.cfg.PollInterval).
		Int("batch_size", w.cfg.BatchSize).
		Msg("Запуск Outbox Worker")

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Остановка Outbox Worker")
			return
		case <-ticker.C:
			w.processOutbox(ctx)
		}
	}
}

// processOutbox обрабатывает пачку необработанных записей.
func (w *OutboxWorker) processOutbox(ctx context.Context) {
	log := logger.FromContext(ctx)

	// Получаем необработанные записи
	records, err := w.repo.GetUnprocessed(ctx, w.cfg.BatchSize)
	if err != nil {
		log.Error().Err(err).Msg("Ошибка чтения outbox")
		return
	}

	if len(records) == 0 {
		return // Нечего обрабатывать
	}

	log.Debug().Int("count", len(records)).Msg("Обработка записей outbox")

	// Обрабатываем каждую запись
	for _, record := range records {
		// Проверяем контекст перед обработкой
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Dead letter: записи с превышенным retry_count помечаем как processed
		// и выводим из очереди. Для ручного разбора: SELECT ... WHERE processed_at IS NOT NULL AND retry_count >= MaxRetries
		if record.RetryCount >= w.cfg.MaxRetries {
			log.Warn().
				Str("outbox_id", record.ID).
				Str("event_type", record.EventType).
				Str("aggregate_id", record.AggregateID).
				Int("retry_count", record.RetryCount).
				Msg("Dead letter: превышен лимит попыток, запись выведена из очереди")

			// Помечаем как processed чтобы убрать из polling
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

		// Помечаем как failed (увеличиваем retry_count)
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
