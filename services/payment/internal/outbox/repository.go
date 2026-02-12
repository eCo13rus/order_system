package outbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// =============================================================================
// Ошибки
// =============================================================================

var ErrOutboxNotFound = errors.New("запись outbox не найдена")

// =============================================================================
// OutboxRepository — интерфейс для работы с таблицей outbox
// =============================================================================

// OutboxRepository определяет методы работы с outbox.
// Интерфейс для тестируемости (Dependency Inversion).
type OutboxRepository interface {
	// Create создаёт новую запись outbox.
	Create(ctx context.Context, record *Outbox) error

	// GetUnprocessed возвращает необработанные записи для отправки в Kafka.
	GetUnprocessed(ctx context.Context, limit int) ([]*Outbox, error)

	// MarkProcessed помечает запись как обработанную.
	MarkProcessed(ctx context.Context, id string) error

	// MarkFailed увеличивает счётчик ошибок и сохраняет текст ошибки.
	MarkFailed(ctx context.Context, id string, err error) error
}

// =============================================================================
// GORM реализация
// =============================================================================

// outboxRepository — GORM реализация OutboxRepository.
type outboxRepository struct {
	db *gorm.DB
}

// NewOutboxRepository создаёт новый репозиторий outbox.
func NewOutboxRepository(db *gorm.DB) OutboxRepository {
	return &outboxRepository{db: db}
}

// Create создаёт новую запись outbox.
func (r *outboxRepository) Create(ctx context.Context, record *Outbox) error {
	model := modelFromDomain(record)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return err
	}
	record.CreatedAt = model.CreatedAt
	return nil
}

// GetUnprocessed возвращает необработанные записи, отсортированные по времени создания.
// Записи с большим retry_count обрабатываются позже (простой backoff).
func (r *outboxRepository) GetUnprocessed(ctx context.Context, limit int) ([]*Outbox, error) {
	var models []OutboxModel

	if err := r.db.WithContext(ctx).
		Where("processed_at IS NULL AND aggregate_type = ?", "payment").
		Order("retry_count ASC, created_at ASC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}

	result := make([]*Outbox, len(models))
	for i := range models {
		result[i] = models[i].toDomain()
	}
	return result, nil
}

// MarkProcessed помечает запись как обработанную.
func (r *outboxRepository) MarkProcessed(ctx context.Context, id string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&OutboxModel{}).
		Where("id = ?", id).
		Update("processed_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOutboxNotFound
	}
	return nil
}

// MarkFailed увеличивает счётчик ошибок и сохраняет текст ошибки.
func (r *outboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	errStr := err.Error()
	result := r.db.WithContext(ctx).Model(&OutboxModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"retry_count": gorm.Expr("retry_count + 1"),
			"last_error":  errStr,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOutboxNotFound
	}
	return nil
}
