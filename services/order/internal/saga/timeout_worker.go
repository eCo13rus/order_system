package saga

import (
	"context"
	"errors"
	"time"

	"example.com/order-system/pkg/logger"
)

// =============================================================================
// SagaTimeoutWorker — воркер для обнаружения и компенсации зависших саг
// =============================================================================

// TimeoutWorkerConfig — настройки Timeout Worker.
type TimeoutWorkerConfig struct {
	// PollInterval — интервал между сканированиями таблицы sagas (30 секунд).
	PollInterval time.Duration

	// SagaTimeout — максимальное время ожидания ответа от Payment Service (5 минут).
	// Саги в PAYMENT_PENDING дольше этого времени считаются зависшими.
	SagaTimeout time.Duration

	// BatchSize — максимальное количество зависших саг за один цикл.
	BatchSize int
}

// DefaultTimeoutWorkerConfig возвращает конфигурацию по умолчанию.
func DefaultTimeoutWorkerConfig() TimeoutWorkerConfig {
	return TimeoutWorkerConfig{
		PollInterval: 30 * time.Second,
		SagaTimeout:  5 * time.Minute,
		BatchSize:    50,
	}
}

// SagaTimeoutWorker периодически сканирует таблицу sagas и находит зависшие саги
// в PAYMENT_PENDING, у которых updated_at старше SagaTimeout.
// Для каждой запускает компенсацию через Orchestrator.CompensateSaga().
type SagaTimeoutWorker struct {
	sagaRepo     SagaRepository
	orchestrator Orchestrator
	cfg          TimeoutWorkerConfig
}

// NewSagaTimeoutWorker создаёт новый Timeout Worker.
func NewSagaTimeoutWorker(sagaRepo SagaRepository, orchestrator Orchestrator, cfg TimeoutWorkerConfig) *SagaTimeoutWorker {
	return &SagaTimeoutWorker{
		sagaRepo:     sagaRepo,
		orchestrator: orchestrator,
		cfg:          cfg,
	}
}

// Run запускает Worker. Блокирует выполнение до отмены контекста.
func (w *SagaTimeoutWorker) Run(ctx context.Context) {
	log := logger.FromContext(ctx)
	log.Info().
		Dur("poll_interval", w.cfg.PollInterval).
		Dur("saga_timeout", w.cfg.SagaTimeout).
		Int("batch_size", w.cfg.BatchSize).
		Msg("Запуск Saga Timeout Worker")

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Остановка Saga Timeout Worker")
			return
		case <-ticker.C:
			w.processStuckSagas(ctx)
		}
	}
}

// processStuckSagas находит и компенсирует зависшие саги.
func (w *SagaTimeoutWorker) processStuckSagas(ctx context.Context) {
	log := logger.FromContext(ctx)

	// Вычисляем порог: саги, не обновлявшиеся дольше SagaTimeout
	stuckSince := time.Now().Add(-w.cfg.SagaTimeout)

	sagas, err := w.sagaRepo.GetStuckSagas(ctx, stuckSince, w.cfg.BatchSize)
	if err != nil {
		log.Error().Err(err).Msg("Ошибка поиска зависших саг")
		return
	}

	if len(sagas) == 0 {
		return
	}

	log.Warn().Int("count", len(sagas)).Msg("Обнаружены зависшие саги, запускаем компенсацию")

	for _, saga := range sagas {
		// Проверяем контекст перед обработкой каждой саги
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Warn().
			Str("saga_id", saga.ID).
			Str("order_id", saga.OrderID).
			Time("updated_at", saga.UpdatedAt).
			Msg("Компенсация зависшей саги по таймауту")

		// CompensateSaga внутри перечитает сагу из БД и проверит статус.
		// Optimistic Locking защитит от race condition с reply consumer.
		err := w.orchestrator.CompensateSaga(ctx, saga.ID, "таймаут ожидания ответа от Payment Service")
		if err != nil {
			// ErrSagaConcurrentUpdate — нормальная ситуация: reply пришёл одновременно с timeout
			if errors.Is(err, ErrSagaConcurrentUpdate) {
				log.Info().
					Str("saga_id", saga.ID).
					Msg("Сага уже обновлена другим процессом (concurrent update), пропускаем")
				continue
			}
			log.Error().Err(err).
				Str("saga_id", saga.ID).
				Msg("Ошибка компенсации зависшей саги")
		}
	}
}
