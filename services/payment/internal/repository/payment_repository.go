// Package repository содержит реализацию доступа к данным для Payment Service.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"example.com/order-system/services/payment/internal/domain"
)

// PaymentRepository определяет интерфейс для работы с платежами в БД.
type PaymentRepository interface {
	// Create создаёт новый платёж.
	Create(ctx context.Context, payment *domain.Payment) error

	// GetByID возвращает платёж по ID.
	GetByID(ctx context.Context, paymentID string) (*domain.Payment, error)

	// GetBySagaID возвращает платёж по ID саги (для идемпотентности).
	GetBySagaID(ctx context.Context, sagaID string) (*domain.Payment, error)

	// Update обновляет платёж.
	Update(ctx context.Context, payment *domain.Payment) error

	// GetStuckPending возвращает платежи в статусе PENDING старше указанного времени.
	GetStuckPending(ctx context.Context, olderThan time.Duration, limit int) ([]*domain.Payment, error)
}

// =============================================================================
// GORM модель
// =============================================================================

// PaymentModel — GORM модель для таблицы payments.
type PaymentModel struct {
	ID             string    `gorm:"column:id;type:varchar(36);primaryKey"`
	OrderID        string    `gorm:"column:order_id;type:varchar(36);not null;index"`
	SagaID         string    `gorm:"column:saga_id;type:varchar(36);not null;index"`
	UserID         string    `gorm:"column:user_id;type:varchar(36);not null;index"`
	Amount         int64     `gorm:"column:amount;not null"`
	Currency       string    `gorm:"column:currency;type:varchar(3);not null"`
	Status         string    `gorm:"column:status;type:varchar(20);not null;index"`
	PaymentMethod  string    `gorm:"column:payment_method;type:varchar(50);not null"`
	FailureReason  *string   `gorm:"column:failure_reason;type:text"`
	RefundID       *string   `gorm:"column:refund_id;type:varchar(36)"`
	RefundReason   *string   `gorm:"column:refund_reason;type:text"`
	IdempotencyKey string    `gorm:"column:idempotency_key;type:varchar(64);not null;uniqueIndex"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName возвращает имя таблицы в БД.
func (PaymentModel) TableName() string {
	return "payments"
}

// toDomain конвертирует GORM модель в доменную сущность.
func (m *PaymentModel) toDomain() *domain.Payment {
	return &domain.Payment{
		ID:             m.ID,
		OrderID:        m.OrderID,
		SagaID:         m.SagaID,
		UserID:         m.UserID,
		Amount:         m.Amount,
		Currency:       m.Currency,
		Status:         domain.PaymentStatus(m.Status),
		PaymentMethod:  m.PaymentMethod,
		FailureReason:  m.FailureReason,
		RefundID:       m.RefundID,
		RefundReason:   m.RefundReason,
		IdempotencyKey: m.IdempotencyKey,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// paymentModelFromDomain конвертирует доменную сущность в GORM модель.
func paymentModelFromDomain(p *domain.Payment) *PaymentModel {
	return &PaymentModel{
		ID:             p.ID,
		OrderID:        p.OrderID,
		SagaID:         p.SagaID,
		UserID:         p.UserID,
		Amount:         p.Amount,
		Currency:       p.Currency,
		Status:         string(p.Status),
		PaymentMethod:  p.PaymentMethod,
		FailureReason:  p.FailureReason,
		RefundID:       p.RefundID,
		RefundReason:   p.RefundReason,
		IdempotencyKey: p.IdempotencyKey,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
}

// =============================================================================
// Реализация репозитория
// =============================================================================

// paymentRepository — GORM реализация PaymentRepository.
type paymentRepository struct {
	db *gorm.DB
}

// NewPaymentRepository создаёт новый репозиторий платежей.
func NewPaymentRepository(db *gorm.DB) PaymentRepository {
	return &paymentRepository{db: db}
}

// Create создаёт новый платёж.
func (r *paymentRepository) Create(ctx context.Context, payment *domain.Payment) error {
	model := paymentModelFromDomain(payment)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		// Проверяем на дубликат idempotency_key
		if isDuplicateKeyError(err) {
			return domain.ErrDuplicatePayment
		}
		return err
	}

	// Обновляем timestamps в доменной сущности
	payment.CreatedAt = model.CreatedAt
	payment.UpdatedAt = model.UpdatedAt

	return nil
}

// GetByID возвращает платёж по ID.
func (r *paymentRepository) GetByID(ctx context.Context, id string) (*domain.Payment, error) {
	var model PaymentModel

	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrPaymentNotFound
		}
		return nil, err
	}

	return model.toDomain(), nil
}

// GetBySagaID возвращает платёж по ID саги.
func (r *paymentRepository) GetBySagaID(ctx context.Context, sagaID string) (*domain.Payment, error) {
	var model PaymentModel

	if err := r.db.WithContext(ctx).
		Where("saga_id = ?", sagaID).
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrPaymentNotFound
		}
		return nil, err
	}

	return model.toDomain(), nil
}

// Update обновляет платёж.
func (r *paymentRepository) Update(ctx context.Context, payment *domain.Payment) error {
	model := paymentModelFromDomain(payment)
	model.UpdatedAt = time.Now()

	result := r.db.WithContext(ctx).
		Model(&PaymentModel{}).
		Where("id = ?", model.ID).
		Updates(map[string]interface{}{
			"status":         model.Status,
			"failure_reason": model.FailureReason,
			"refund_id":      model.RefundID,
			"refund_reason":  model.RefundReason,
			"updated_at":     model.UpdatedAt,
		})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return domain.ErrPaymentNotFound
	}

	payment.UpdatedAt = model.UpdatedAt
	return nil
}

// GetStuckPending возвращает платежи в статусе PENDING старше указанного времени.
func (r *paymentRepository) GetStuckPending(ctx context.Context, olderThan time.Duration, limit int) ([]*domain.Payment, error) {
	var models []PaymentModel

	threshold := time.Now().Add(-olderThan)

	if err := r.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", string(domain.PaymentStatusPending), threshold).
		Order("created_at ASC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}

	payments := make([]*domain.Payment, 0, len(models))
	for _, m := range models {
		payments = append(payments, m.toDomain())
	}

	return payments, nil
}

// isDuplicateKeyError проверяет, является ли ошибка дубликатом ключа.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errMsg, "Duplicate entry") ||
		strings.Contains(errMsg, "1062")
}
