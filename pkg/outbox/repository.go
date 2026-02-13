package outbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrOutboxNotFound — запись outbox не найдена.
var ErrOutboxNotFound = errors.New("запись outbox не найдена")

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

	// DeleteProcessedBefore удаляет обработанные записи старше указанного времени.
	// Возвращает количество удалённых записей. Используется для очистки outbox.
	DeleteProcessedBefore(ctx context.Context, before time.Time) (int64, error)
}

// outboxRepository — GORM реализация OutboxRepository.
// aggregateType фильтрует записи по типу агрегата ("order" / "payment").
type outboxRepository struct {
	db            *gorm.DB
	aggregateType string
}

// NewOutboxRepository создаёт новый репозиторий outbox.
// aggregateType — тип агрегата для фильтрации ("order" / "payment").
func NewOutboxRepository(db *gorm.DB, aggregateType string) OutboxRepository {
	return &outboxRepository{db: db, aggregateType: aggregateType}
}

// Create создаёт новую запись outbox.
func (r *outboxRepository) Create(ctx context.Context, record *Outbox) error {
	model := ModelFromDomain(record)
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
		Where("processed_at IS NULL AND aggregate_type = ?", r.aggregateType).
		Order("retry_count ASC, created_at ASC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}

	result := make([]*Outbox, len(models))
	for i := range models {
		result[i] = models[i].ToDomain()
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

// DeleteProcessedBefore удаляет обработанные записи outbox старше указанного времени.
// Удаляет пачками по 1000 для предотвращения длинных блокировок.
func (r *outboxRepository) DeleteProcessedBefore(ctx context.Context, before time.Time) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("processed_at IS NOT NULL AND processed_at < ? AND aggregate_type = ?", before, r.aggregateType).
		Limit(1000).
		Delete(&OutboxModel{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
