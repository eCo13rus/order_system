package saga

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"example.com/order-system/services/order/internal/domain"
)

// =============================================================================
// Ошибки репозитория
// =============================================================================

var (
	ErrSagaNotFound   = errors.New("сага не найдена")
	ErrOutboxNotFound = errors.New("запись outbox не найдена")
)

// =============================================================================
// GORM модели
// =============================================================================

// SagaModel — GORM модель для таблицы sagas.
type SagaModel struct {
	ID            string    `gorm:"column:id;type:varchar(36);primaryKey"`
	OrderID       string    `gorm:"column:order_id;type:varchar(36);not null;uniqueIndex"`
	Status        string    `gorm:"column:status;type:varchar(20);not null;index"`
	StepData      []byte    `gorm:"column:step_data;type:json"`
	FailureReason *string   `gorm:"column:failure_reason;type:text"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt     time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (SagaModel) TableName() string {
	return "sagas"
}

// toDomain конвертирует GORM модель в доменную сущность.
func (m *SagaModel) toDomain() *Saga {
	saga := &Saga{
		ID:            m.ID,
		OrderID:       m.OrderID,
		Status:        Status(m.Status),
		FailureReason: m.FailureReason,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}

	// Десериализуем step_data из JSON
	if len(m.StepData) > 0 {
		var stepData StepData
		if err := json.Unmarshal(m.StepData, &stepData); err == nil {
			saga.StepData = &stepData
		}
	}

	return saga
}

// sagaModelFromDomain конвертирует доменную сущность в GORM модель.
func sagaModelFromDomain(s *Saga) *SagaModel {
	model := &SagaModel{
		ID:            s.ID,
		OrderID:       s.OrderID,
		Status:        string(s.Status),
		FailureReason: s.FailureReason,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
	}

	// Сериализуем step_data в JSON
	if s.StepData != nil {
		if data, err := json.Marshal(s.StepData); err == nil {
			model.StepData = data
		}
	}

	return model
}

// OutboxModel — GORM модель для таблицы outbox.
type OutboxModel struct {
	ID            string     `gorm:"column:id;type:varchar(36);primaryKey"`
	AggregateType string     `gorm:"column:aggregate_type;type:varchar(50);not null;index:idx_outbox_aggregate"`
	AggregateID   string     `gorm:"column:aggregate_id;type:varchar(36);not null;index:idx_outbox_aggregate"`
	EventType     string     `gorm:"column:event_type;type:varchar(100);not null"`
	Topic         string     `gorm:"column:topic;type:varchar(100);not null"`
	MessageKey    string     `gorm:"column:message_key;type:varchar(100);not null"`
	Payload       []byte     `gorm:"column:payload;type:json;not null"`
	Headers       []byte     `gorm:"column:headers;type:json"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime"`
	ProcessedAt   *time.Time `gorm:"column:processed_at;index:idx_outbox_unprocessed"`
	RetryCount    int        `gorm:"column:retry_count;not null;default:0;index:idx_outbox_retry"`
	LastError     *string    `gorm:"column:last_error;type:text"`
}

func (OutboxModel) TableName() string {
	return "outbox"
}

// toDomain конвертирует GORM модель в доменную сущность.
func (m *OutboxModel) toDomain() *Outbox {
	outbox := &Outbox{
		ID:            m.ID,
		AggregateType: m.AggregateType,
		AggregateID:   m.AggregateID,
		EventType:     m.EventType,
		Topic:         m.Topic,
		MessageKey:    m.MessageKey,
		Payload:       m.Payload,
		CreatedAt:     m.CreatedAt,
		ProcessedAt:   m.ProcessedAt,
		RetryCount:    m.RetryCount,
		LastError:     m.LastError,
	}

	// Десериализуем headers из JSON
	if len(m.Headers) > 0 {
		_ = outbox.SetHeadersFromJSON(m.Headers)
	}

	return outbox
}

// outboxModelFromDomain конвертирует доменную сущность в GORM модель.
func outboxModelFromDomain(o *Outbox) *OutboxModel {
	model := &OutboxModel{
		ID:            o.ID,
		AggregateType: o.AggregateType,
		AggregateID:   o.AggregateID,
		EventType:     o.EventType,
		Topic:         o.Topic,
		MessageKey:    o.MessageKey,
		Payload:       o.Payload,
		CreatedAt:     o.CreatedAt,
		ProcessedAt:   o.ProcessedAt,
		RetryCount:    o.RetryCount,
		LastError:     o.LastError,
	}

	// Сериализуем headers в JSON
	if o.Headers != nil {
		if data, err := o.HeadersJSON(); err == nil {
			model.Headers = data
		}
	}

	return model
}

// =============================================================================
// SagaRepository — интерфейс для работы с таблицей sagas
// =============================================================================

// SagaRepository определяет методы работы с сагами.
// Интерфейс минимизирован — содержит только реально используемые методы (ISP).
type SagaRepository interface {
	// GetByID возвращает сагу по ID.
	GetByID(ctx context.Context, id string) (*Saga, error)

	// CreateWithOutbox создаёт сагу и запись outbox в одной транзакции.
	// Это ключевой метод для Outbox Pattern — атомарность бизнес-данных и события.
	CreateWithOutbox(ctx context.Context, saga *Saga, outbox *Outbox) error

	// CreateOrderWithSagaAndOutbox атомарно создаёт заказ, сагу и outbox запись.
	// КРИТИЧНО: решает проблему dual write — всё в одной транзакции.
	// Если любая часть падает — откатывается вся транзакция.
	CreateOrderWithSagaAndOutbox(ctx context.Context, order *domain.Order, saga *Saga, outbox *Outbox) error

	// UpdateWithOrder атомарно обновляет сагу и статус заказа в одной транзакции.
	// Гарантирует консистентность данных между saga и order.
	UpdateWithOrder(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string) error

	// UpdateWithOrderAndOutbox атомарно обновляет сагу, заказ и создаёт outbox запись.
	// Используется для компенсации с refund — гарантирует, что refund не дублируется при retry.
	// outbox может быть nil, если refund не требуется.
	UpdateWithOrderAndOutbox(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string, outbox *Outbox) error
}

// =============================================================================
// OutboxRepository — интерфейс для работы с таблицей outbox
// =============================================================================

// OutboxRepository определяет методы работы с outbox.
type OutboxRepository interface {
	// Create создаёт новую запись outbox.
	Create(ctx context.Context, outbox *Outbox) error

	// GetUnprocessed возвращает необработанные записи для отправки в Kafka.
	// limit ограничивает количество записей за один запрос.
	GetUnprocessed(ctx context.Context, limit int) ([]*Outbox, error)

	// MarkProcessed помечает запись как обработанную.
	MarkProcessed(ctx context.Context, id string) error

	// MarkFailed увеличивает счётчик ошибок и сохраняет текст ошибки.
	MarkFailed(ctx context.Context, id string, err error) error
}

// =============================================================================
// Реализации репозиториев
// =============================================================================

// sagaRepository — GORM реализация SagaRepository.
type sagaRepository struct {
	db *gorm.DB
}

// NewSagaRepository создаёт новый репозиторий саг.
func NewSagaRepository(db *gorm.DB) SagaRepository {
	return &sagaRepository{db: db}
}

func (r *sagaRepository) GetByID(ctx context.Context, id string) (*Saga, error) {
	var model SagaModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSagaNotFound
		}
		return nil, err
	}
	return model.toDomain(), nil
}

// CreateWithOutbox — атомарное создание саги и записи outbox.
func (r *sagaRepository) CreateWithOutbox(ctx context.Context, saga *Saga, outbox *Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Создаём сагу
		sagaModel := sagaModelFromDomain(saga)
		if err := tx.Create(sagaModel).Error; err != nil {
			return err
		}
		saga.CreatedAt = sagaModel.CreatedAt
		saga.UpdatedAt = sagaModel.UpdatedAt

		// Создаём запись outbox
		outboxModel := outboxModelFromDomain(outbox)
		if err := tx.Create(outboxModel).Error; err != nil {
			return err
		}
		outbox.CreatedAt = outboxModel.CreatedAt

		return nil
	})
}

// CreateOrderWithSagaAndOutbox — атомарное создание заказа, саги и outbox.
// КРИТИЧНО: решает проблему dual write — order+saga+outbox в одной транзакции.
// Если любая часть падает — откатывается ВСЁ.
func (r *sagaRepository) CreateOrderWithSagaAndOutbox(ctx context.Context, order *domain.Order, saga *Saga, outbox *Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// 1. Создаём заказ
		orderData := map[string]interface{}{
			"id":              order.ID,
			"user_id":         order.UserID,
			"status":          string(order.Status),
			"total_amount":    order.TotalAmount.Amount,
			"currency":        order.TotalAmount.Currency,
			"idempotency_key": nullableString(order.IdempotencyKey),
			"created_at":      now,
			"updated_at":      now,
		}
		if err := tx.Table("orders").Create(orderData).Error; err != nil {
			return err
		}

		// 2. Создаём позиции заказа
		for i := range order.Items {
			item := &order.Items[i]
			itemData := map[string]interface{}{
				"id":           item.ID,
				"order_id":     order.ID,
				"product_id":   item.ProductID,
				"product_name": item.ProductName,
				"quantity":     item.Quantity,
				"unit_price":   item.UnitPrice.Amount,
				"currency":     item.UnitPrice.Currency,
				"created_at":   now,
				"updated_at":   now,
			}
			if err := tx.Table("order_items").Create(itemData).Error; err != nil {
				return err
			}
		}

		// 3. Создаём сагу
		sagaModel := sagaModelFromDomain(saga)
		if err := tx.Create(sagaModel).Error; err != nil {
			return err
		}
		saga.CreatedAt = sagaModel.CreatedAt
		saga.UpdatedAt = sagaModel.UpdatedAt

		// 4. Создаём запись outbox
		outboxModel := outboxModelFromDomain(outbox)
		if err := tx.Create(outboxModel).Error; err != nil {
			return err
		}
		outbox.CreatedAt = outboxModel.CreatedAt

		// Обновляем timestamps в order
		order.CreatedAt = now
		order.UpdatedAt = now

		return nil
	})
}

// nullableString возвращает nil для пустой строки.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UpdateWithOrder — атомарное обновление саги и статуса заказа.
// Гарантирует консистентность: либо обе записи обновляются, либо ни одна.
func (r *sagaRepository) UpdateWithOrder(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// Обновляем сагу
		sagaModel := sagaModelFromDomain(saga)
		sagaResult := tx.Model(&SagaModel{}).
			Where("id = ?", saga.ID).
			Updates(map[string]interface{}{
				"status":         sagaModel.Status,
				"step_data":      sagaModel.StepData,
				"failure_reason": sagaModel.FailureReason,
				"updated_at":     now,
			})
		if sagaResult.Error != nil {
			return sagaResult.Error
		}
		if sagaResult.RowsAffected == 0 {
			return ErrSagaNotFound
		}

		// Обновляем заказ (используем ту же таблицу orders)
		orderResult := tx.Table("orders").
			Where("id = ?", orderID).
			Updates(map[string]interface{}{
				"status":         string(orderStatus),
				"payment_id":     paymentID,
				"failure_reason": failureReason,
				"updated_at":     now,
			})
		if orderResult.Error != nil {
			return orderResult.Error
		}
		if orderResult.RowsAffected == 0 {
			return domain.ErrOrderNotFound
		}

		return nil
	})
}

// UpdateWithOrderAndOutbox — атомарное обновление саги, заказа и создание outbox записи.
// Решает проблему дублирования refund при retry: всё в одной транзакции.
// Если outbox == nil, создание outbox пропускается (refund не нужен).
func (r *sagaRepository) UpdateWithOrderAndOutbox(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string, outbox *Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// 1. Обновляем сагу
		sagaModel := sagaModelFromDomain(saga)
		sagaResult := tx.Model(&SagaModel{}).
			Where("id = ?", saga.ID).
			Updates(map[string]interface{}{
				"status":         sagaModel.Status,
				"step_data":      sagaModel.StepData,
				"failure_reason": sagaModel.FailureReason,
				"updated_at":     now,
			})
		if sagaResult.Error != nil {
			return sagaResult.Error
		}
		if sagaResult.RowsAffected == 0 {
			return ErrSagaNotFound
		}

		// 2. Обновляем заказ
		orderResult := tx.Table("orders").
			Where("id = ?", orderID).
			Updates(map[string]interface{}{
				"status":         string(orderStatus),
				"payment_id":     paymentID,
				"failure_reason": failureReason,
				"updated_at":     now,
			})
		if orderResult.Error != nil {
			return orderResult.Error
		}
		if orderResult.RowsAffected == 0 {
			return domain.ErrOrderNotFound
		}

		// 3. Создаём outbox запись для refund (если нужна)
		if outbox != nil {
			outboxModel := outboxModelFromDomain(outbox)
			if err := tx.Create(outboxModel).Error; err != nil {
				return err
			}
			outbox.CreatedAt = outboxModel.CreatedAt
		}

		return nil
	})
}

// outboxRepository — GORM реализация OutboxRepository.
type outboxRepository struct {
	db *gorm.DB
}

// NewOutboxRepository создаёт новый репозиторий outbox.
func NewOutboxRepository(db *gorm.DB) OutboxRepository {
	return &outboxRepository{db: db}
}

func (r *outboxRepository) Create(ctx context.Context, outbox *Outbox) error {
	model := outboxModelFromDomain(outbox)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return err
	}
	outbox.CreatedAt = model.CreatedAt
	return nil
}

// GetUnprocessed возвращает необработанные записи, отсортированные по времени создания.
// Записи с большим retry_count обрабатываются позже (простой backoff на уровне выборки).
func (r *outboxRepository) GetUnprocessed(ctx context.Context, limit int) ([]*Outbox, error) {
	var models []OutboxModel

	// Выбираем записи где processed_at IS NULL, сортируем по created_at
	// Записи с retry_count > 0 откладываем (простая стратегия backoff)
	if err := r.db.WithContext(ctx).
		Where("processed_at IS NULL").
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

func (r *outboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	errStr := err.Error()
	result := r.db.WithContext(ctx).Model(&OutboxModel{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
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
