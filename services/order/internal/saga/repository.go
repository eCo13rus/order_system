package saga

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"example.com/order-system/pkg/logger"
	outboxpkg "example.com/order-system/pkg/outbox"
	"example.com/order-system/services/order/internal/domain"
)

// =============================================================================
// Ошибки репозитория
// =============================================================================

var (
	ErrSagaNotFound         = errors.New("сага не найдена")
	ErrSagaConcurrentUpdate = errors.New("конкурентное обновление саги (optimistic lock)")
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
	Version       int       `gorm:"column:version;not null;default:1"`
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
		Version:       m.Version,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}

	// Десериализуем step_data из JSON
	if len(m.StepData) > 0 {
		var stepData StepData
		if err := json.Unmarshal(m.StepData, &stepData); err != nil {
			logger.Error().Err(err).Str("saga_id", m.ID).Msg("Ошибка десериализации step_data саги")
		} else {
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
		Version:       s.Version,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
	}

	// Сериализуем step_data в JSON
	if s.StepData != nil {
		if data, err := json.Marshal(s.StepData); err != nil {
			logger.Error().Err(err).Str("saga_id", s.ID).Msg("Ошибка сериализации step_data саги")
		} else {
			model.StepData = data
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

	// GetByOrderID возвращает сагу по ID заказа.
	// Используется для проверки активной саги перед отменой заказа.
	GetByOrderID(ctx context.Context, orderID string) (*Saga, error)

	// CreateWithOutbox создаёт сагу и запись outbox в одной транзакции.
	// Это ключевой метод для Outbox Pattern — атомарность бизнес-данных и события.
	CreateWithOutbox(ctx context.Context, saga *Saga, outbox *outboxpkg.Outbox) error

	// CreateOrderWithSagaAndOutbox атомарно создаёт заказ, сагу и outbox запись.
	// КРИТИЧНО: решает проблему dual write — всё в одной транзакции.
	// Если любая часть падает — откатывается вся транзакция.
	CreateOrderWithSagaAndOutbox(ctx context.Context, order *domain.Order, saga *Saga, outbox *outboxpkg.Outbox) error

	// UpdateWithOrder атомарно обновляет сагу и статус заказа в одной транзакции.
	// Гарантирует консистентность данных между saga и order.
	UpdateWithOrder(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string) error

	// UpdateWithOrderAndOutbox атомарно обновляет сагу, заказ и создаёт outbox запись.
	// Используется для компенсации с refund — гарантирует, что refund не дублируется при retry.
	// outbox может быть nil, если refund не требуется.
	UpdateWithOrderAndOutbox(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string, outbox *outboxpkg.Outbox) error

	// GetStuckSagas возвращает саги в PAYMENT_PENDING, которые не обновлялись дольше stuckSince.
	// Используется Timeout Worker для обнаружения зависших саг.
	GetStuckSagas(ctx context.Context, stuckSince time.Time, limit int) ([]*Saga, error)
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

// GetByOrderID возвращает сагу по order_id (unique index).
func (r *sagaRepository) GetByOrderID(ctx context.Context, orderID string) (*Saga, error) {
	var model SagaModel
	if err := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSagaNotFound
		}
		return nil, err
	}
	return model.toDomain(), nil
}

// CreateWithOutbox — атомарное создание саги и записи outbox.
func (r *sagaRepository) CreateWithOutbox(ctx context.Context, saga *Saga, outbox *outboxpkg.Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Создаём сагу
		sagaModel := sagaModelFromDomain(saga)
		if err := tx.Create(sagaModel).Error; err != nil {
			return err
		}
		saga.CreatedAt = sagaModel.CreatedAt
		saga.UpdatedAt = sagaModel.UpdatedAt

		// Создаём запись outbox
		outboxModel := outboxpkg.ModelFromDomain(outbox)
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
func (r *sagaRepository) CreateOrderWithSagaAndOutbox(ctx context.Context, order *domain.Order, saga *Saga, outbox *outboxpkg.Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// 1. Создаём заказ
		orderData := map[string]any{
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
			itemData := map[string]any{
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
		outboxModel := outboxpkg.ModelFromDomain(outbox)
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
// Optimistic Locking: WHERE version = ? + version = version + 1.
func (r *sagaRepository) UpdateWithOrder(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// Обновляем сагу с проверкой версии (Optimistic Locking)
		sagaModel := sagaModelFromDomain(saga)
		sagaResult := tx.Model(&SagaModel{}).
			Where("id = ? AND version = ?", saga.ID, saga.Version).
			Updates(map[string]any{
				"status":         sagaModel.Status,
				"step_data":      sagaModel.StepData,
				"failure_reason": sagaModel.FailureReason,
				"version":        gorm.Expr("version + 1"),
				"updated_at":     now,
			})
		if sagaResult.Error != nil {
			return sagaResult.Error
		}
		if sagaResult.RowsAffected == 0 {
			return ErrSagaConcurrentUpdate
		}

		// Обновляем заказ (используем ту же таблицу orders)
		orderResult := tx.Table("orders").
			Where("id = ?", orderID).
			Updates(map[string]any{
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
// Optimistic Locking: WHERE version = ? + version = version + 1.
func (r *sagaRepository) UpdateWithOrderAndOutbox(ctx context.Context, saga *Saga, orderID string, orderStatus domain.OrderStatus, paymentID, failureReason *string, outbox *outboxpkg.Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// 1. Обновляем сагу с проверкой версии (Optimistic Locking)
		sagaModel := sagaModelFromDomain(saga)
		sagaResult := tx.Model(&SagaModel{}).
			Where("id = ? AND version = ?", saga.ID, saga.Version).
			Updates(map[string]any{
				"status":         sagaModel.Status,
				"step_data":      sagaModel.StepData,
				"failure_reason": sagaModel.FailureReason,
				"version":        gorm.Expr("version + 1"),
				"updated_at":     now,
			})
		if sagaResult.Error != nil {
			return sagaResult.Error
		}
		if sagaResult.RowsAffected == 0 {
			return ErrSagaConcurrentUpdate
		}

		// 2. Обновляем заказ
		orderResult := tx.Table("orders").
			Where("id = ?", orderID).
			Updates(map[string]any{
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
			outboxModel := outboxpkg.ModelFromDomain(outbox)
			if err := tx.Create(outboxModel).Error; err != nil {
				return err
			}
			outbox.CreatedAt = outboxModel.CreatedAt
		}

		return nil
	})
}

// =============================================================================
// GetStuckSagas — поиск зависших саг для Timeout Worker
// =============================================================================

// GetStuckSagas возвращает саги в PAYMENT_PENDING, не обновлявшиеся с stuckSince.
func (r *sagaRepository) GetStuckSagas(ctx context.Context, stuckSince time.Time, limit int) ([]*Saga, error) {
	var models []SagaModel

	if err := r.db.WithContext(ctx).
		Where("status = ? AND updated_at < ?", string(StatusPaymentPending), stuckSince).
		Order("updated_at ASC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}

	result := make([]*Saga, len(models))
	for i := range models {
		result[i] = models[i].toDomain()
	}
	return result, nil
}
