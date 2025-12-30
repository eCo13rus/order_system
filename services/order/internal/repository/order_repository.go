// Package repository содержит реализацию доступа к данным для Order Service.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"example.com/order-system/services/order/internal/domain"
)

// OrderRepository определяет интерфейс для работы с заказами в БД.
type OrderRepository interface {
	// Create создаёт новый заказ с позициями.
	// Выполняется в транзакции для атомарности.
	Create(ctx context.Context, order *domain.Order) error

	// GetByID возвращает заказ по ID с загруженными позициями.
	GetByID(ctx context.Context, orderID string) (*domain.Order, error)

	// GetByIdempotencyKey возвращает заказ по ключу идемпотентности.
	// Используется для предотвращения дублирования заказов.
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.Order, error)

	// ListByUserID возвращает заказы пользователя с пагинацией.
	// status может быть nil для получения заказов во всех статусах.
	// Возвращает список заказов и общее количество (для пагинации).
	ListByUserID(ctx context.Context, userID string, status *domain.OrderStatus, offset, limit int) ([]*domain.Order, int64, error)

	// UpdateStatus атомарно обновляет статус заказа.
	// paymentID устанавливается при подтверждении оплаты.
	// failureReason устанавливается при ошибке/отмене.
	UpdateStatus(ctx context.Context, orderID string, status domain.OrderStatus, paymentID, failureReason *string) error
}

// OrderModel — GORM модель для таблицы orders.
// Отделена от доменной сущности для гибкости.
type OrderModel struct {
	ID             string           `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID         string           `gorm:"column:user_id;type:varchar(36);not null;index"`
	Status         string           `gorm:"column:status;type:varchar(20);not null;index"`
	TotalAmount    int64            `gorm:"column:total_amount;not null"`
	Currency       string           `gorm:"column:currency;type:varchar(3);not null"`
	IdempotencyKey *string          `gorm:"column:idempotency_key;type:varchar(64);uniqueIndex"`
	PaymentID      *string          `gorm:"column:payment_id;type:varchar(36)"`
	FailureReason  *string          `gorm:"column:failure_reason;type:text"`
	CreatedAt      time.Time        `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt      time.Time        `gorm:"column:updated_at;autoUpdateTime"`
	Items          []OrderItemModel `gorm:"foreignKey:OrderID;references:ID"`
}

// TableName возвращает имя таблицы в БД.
func (OrderModel) TableName() string {
	return "orders"
}

// OrderItemModel — GORM модель для таблицы order_items.
type OrderItemModel struct {
	ID          string    `gorm:"column:id;type:varchar(36);primaryKey"`
	OrderID     string    `gorm:"column:order_id;type:varchar(36);not null;index"`
	ProductID   string    `gorm:"column:product_id;type:varchar(36);not null"`
	ProductName string    `gorm:"column:product_name;type:varchar(255);not null"`
	Quantity    int32     `gorm:"column:quantity;not null"`
	UnitPrice   int64     `gorm:"column:unit_price;not null"`
	Currency    string    `gorm:"column:currency;type:varchar(3);not null"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName возвращает имя таблицы в БД.
func (OrderItemModel) TableName() string {
	return "order_items"
}

// toDomain конвертирует GORM модель заказа в доменную сущность.
func (m *OrderModel) toDomain() *domain.Order {
	order := &domain.Order{
		ID:     m.ID,
		UserID: m.UserID,
		Status: domain.OrderStatus(m.Status),
		TotalAmount: domain.Money{
			Amount:   m.TotalAmount,
			Currency: m.Currency,
		},
		PaymentID:     m.PaymentID,
		FailureReason: m.FailureReason,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
		Items:         make([]domain.OrderItem, len(m.Items)),
	}

	// Обработка idempotency_key
	if m.IdempotencyKey != nil {
		order.IdempotencyKey = *m.IdempotencyKey
	}

	// Конвертируем позиции заказа
	for i, item := range m.Items {
		order.Items[i] = *item.toDomain()
	}

	return order
}

// toDomain конвертирует GORM модель позиции в доменную сущность.
func (m *OrderItemModel) toDomain() *domain.OrderItem {
	return &domain.OrderItem{
		ID:          m.ID,
		OrderID:     m.OrderID,
		ProductID:   m.ProductID,
		ProductName: m.ProductName,
		Quantity:    m.Quantity,
		UnitPrice: domain.Money{
			Amount:   m.UnitPrice,
			Currency: m.Currency,
		},
	}
}

// orderModelFromDomain конвертирует доменную сущность заказа в GORM модель.
func orderModelFromDomain(o *domain.Order) *OrderModel {
	model := &OrderModel{
		ID:            o.ID,
		UserID:        o.UserID,
		Status:        string(o.Status),
		TotalAmount:   o.TotalAmount.Amount,
		Currency:      o.TotalAmount.Currency,
		PaymentID:     o.PaymentID,
		FailureReason: o.FailureReason,
		CreatedAt:     o.CreatedAt,
		UpdatedAt:     o.UpdatedAt,
		Items:         make([]OrderItemModel, len(o.Items)),
	}

	// Обработка idempotency_key: пустая строка -> nil
	if o.IdempotencyKey != "" {
		model.IdempotencyKey = &o.IdempotencyKey
	}

	// Конвертируем позиции заказа
	for i, item := range o.Items {
		model.Items[i] = *orderItemModelFromDomain(&item)
	}

	return model
}

// orderItemModelFromDomain конвертирует доменную сущность позиции в GORM модель.
func orderItemModelFromDomain(oi *domain.OrderItem) *OrderItemModel {
	return &OrderItemModel{
		ID:          oi.ID,
		OrderID:     oi.OrderID,
		ProductID:   oi.ProductID,
		ProductName: oi.ProductName,
		Quantity:    oi.Quantity,
		UnitPrice:   oi.UnitPrice.Amount,
		Currency:    oi.UnitPrice.Currency,
	}
}

// orderRepository — GORM реализация OrderRepository.
type orderRepository struct {
	db *gorm.DB
}

// NewOrderRepository создаёт новый репозиторий заказов.
func NewOrderRepository(db *gorm.DB) OrderRepository {
	return &orderRepository{db: db}
}

// Create создаёт новый заказ с позициями в одной транзакции.
func (r *orderRepository) Create(ctx context.Context, order *domain.Order) error {
	model := orderModelFromDomain(order)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Создаём заказ (без позиций, GORM создаст их автоматически через ассоциацию)
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		// Проверяем на дубликат idempotency_key (MySQL error 1062)
		if isDuplicateKeyError(err) {
			return domain.ErrDuplicateOrder
		}
		return err
	}

	// Обновляем timestamps в доменной сущности
	order.CreatedAt = model.CreatedAt
	order.UpdatedAt = model.UpdatedAt

	// Обновляем timestamps в позициях
	for i := range order.Items {
		order.Items[i].ID = model.Items[i].ID
	}

	return nil
}

// GetByID возвращает заказ по ID с загруженными позициями.
func (r *orderRepository) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	var model OrderModel

	if err := r.db.WithContext(ctx).
		Preload("Items").
		Where("id = ?", id).
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, err
	}

	return model.toDomain(), nil
}

// GetByIdempotencyKey возвращает заказ по ключу идемпотентности.
func (r *orderRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	var model OrderModel

	if err := r.db.WithContext(ctx).
		Preload("Items").
		Where("idempotency_key = ?", key).
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, err
	}

	return model.toDomain(), nil
}

// ListByUserID возвращает список заказов пользователя с пагинацией.
// Опциональный фильтр по статусу, возвращает также общее количество записей.
func (r *orderRepository) ListByUserID(ctx context.Context, userID string, status *domain.OrderStatus, offset, limit int) ([]*domain.Order, int64, error) {
	var models []OrderModel
	var totalCount int64

	// Базовый запрос
	query := r.db.WithContext(ctx).Model(&OrderModel{}).Where("user_id = ?", userID)

	// Опциональный фильтр по статусу
	if status != nil {
		query = query.Where("status = ?", string(*status))
	}

	// Подсчёт общего количества записей (до пагинации)
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	// Пагинация и сортировка (новые заказы первыми)
	if err := query.
		Preload("Items").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	// Конвертируем в доменные сущности
	orders := make([]*domain.Order, len(models))
	for i := range models {
		orders[i] = models[i].toDomain()
	}

	return orders, totalCount, nil
}

// UpdateStatus атомарно обновляет статус заказа.
func (r *orderRepository) UpdateStatus(ctx context.Context, id string, status domain.OrderStatus, paymentID, failureReason *string) error {
	updates := map[string]interface{}{
		"status":     string(status),
		"updated_at": time.Now(),
	}

	// Добавляем опциональные поля
	if paymentID != nil {
		updates["payment_id"] = *paymentID
	}
	if failureReason != nil {
		updates["failure_reason"] = *failureReason
	}

	result := r.db.WithContext(ctx).
		Model(&OrderModel{}).
		Where("id = ?", id).
		Updates(updates)

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return domain.ErrOrderNotFound
	}

	return nil
}

// isDuplicateKeyError проверяет, является ли ошибка дубликатом ключа.
// MySQL возвращает ошибку с кодом 1062 при попытке вставить дубликат.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// GORM v2 имеет ErrDuplicatedKey, также проверяем текст ошибки MySQL
	errMsg := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errMsg, "Duplicate entry") ||
		strings.Contains(errMsg, "1062")
}
