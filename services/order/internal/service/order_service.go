// Package service содержит бизнес-логику Order Service.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/order/internal/domain"
	"example.com/order-system/services/order/internal/repository"
	"example.com/order-system/services/order/internal/saga"
)

// Константы для валидации пагинации.
const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
	minPageSize     = 1
)

// OrderService определяет интерфейс бизнес-логики заказов.
type OrderService interface {
	// CreateOrder создаёт новый заказ с идемпотентностью.
	// Если заказ с таким idempotencyKey уже существует, возвращает существующий заказ.
	CreateOrder(ctx context.Context, userID, idempotencyKey string, items []domain.OrderItem) (*domain.Order, error)

	// GetOrder возвращает заказ по ID.
	GetOrder(ctx context.Context, orderID string) (*domain.Order, error)

	// ListOrders возвращает заказы пользователя с пагинацией.
	// status может быть nil для получения всех заказов.
	// Возвращает список заказов и общее количество записей.
	ListOrders(ctx context.Context, userID string, status *domain.OrderStatus, page, pageSize int) ([]*domain.Order, int64, error)

	// CancelOrder отменяет заказ.
	CancelOrder(ctx context.Context, orderID string) error
}

// orderService — реализация OrderService.
type orderService struct {
	repo         repository.OrderRepository
	orchestrator saga.Orchestrator // Saga Orchestrator для запуска распределённых транзакций
}

// NewOrderService создаёт новый сервис заказов.
// orchestrator может быть nil — тогда саги не запускаются (для тестов без саги).
func NewOrderService(repo repository.OrderRepository, orchestrator saga.Orchestrator) OrderService {
	return &orderService{
		repo:         repo,
		orchestrator: orchestrator,
	}
}

// CreateOrder создаёт новый заказ с идемпотентностью.
func (s *orderService) CreateOrder(ctx context.Context, userID, idempotencyKey string, items []domain.OrderItem) (*domain.Order, error) {
	log := logger.FromContext(ctx)

	// Проверяем идемпотентность — если заказ с таким ключом существует, возвращаем его
	if idempotencyKey != "" {
		existingOrder, err := s.repo.GetByIdempotencyKey(ctx, idempotencyKey)
		if err == nil && existingOrder != nil {
			log.Info().
				Str("order_id", existingOrder.ID).
				Str("idempotency_key", idempotencyKey).
				Msg("Возвращён существующий заказ по ключу идемпотентности")
			return existingOrder, nil
		}
		// Если ошибка не ErrOrderNotFound — это реальная ошибка
		if err != nil && !errors.Is(err, domain.ErrOrderNotFound) {
			log.Error().Err(err).Str("idempotency_key", idempotencyKey).Msg("Ошибка проверки идемпотентности")
			return nil, fmt.Errorf("ошибка проверки идемпотентности: %w", err)
		}
	}

	// Генерируем ID для заказа и позиций
	orderID := uuid.New().String()
	now := time.Now()

	// Копируем items и назначаем ID для каждой позиции
	orderItems := make([]domain.OrderItem, len(items))
	for i := range items {
		orderItems[i] = items[i]
		orderItems[i].ID = uuid.New().String()
		orderItems[i].OrderID = orderID
	}

	// Создаём доменную сущность
	order := &domain.Order{
		ID:             orderID,
		UserID:         userID,
		Items:          orderItems,
		Status:         domain.OrderStatusPending,
		IdempotencyKey: idempotencyKey,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Валидация заказа
	if err := order.Validate(); err != nil {
		log.Warn().
			Err(err).
			Str("user_id", userID).
			Msg("Ошибка валидации заказа")
		return nil, err
	}

	// Пересчитываем итоговую сумму
	order.CalculateTotal()

	// Создаём заказ с сагой АТОМАРНО (если orchestrator настроен)
	// Это решает проблему dual write — order+saga+outbox в одной транзакции
	if s.orchestrator != nil {
		if err := s.orchestrator.CreateOrderWithSaga(ctx, order); err != nil {
			log.Error().
				Err(err).
				Str("user_id", userID).
				Str("idempotency_key", idempotencyKey).
				Msg("Ошибка создания заказа с сагой")
			return nil, fmt.Errorf("ошибка создания заказа: %w", err)
		}
	} else {
		// Без orchestrator — создаём только заказ (для тестов)
		if err := s.repo.Create(ctx, order); err != nil {
			log.Error().
				Err(err).
				Str("user_id", userID).
				Str("idempotency_key", idempotencyKey).
				Msg("Ошибка создания заказа")
			return nil, fmt.Errorf("ошибка создания заказа: %w", err)
		}
	}

	log.Info().
		Str("order_id", order.ID).
		Str("user_id", userID).
		Int64("total_amount", order.TotalAmount.Amount).
		Str("currency", order.TotalAmount.Currency).
		Int("items_count", len(order.Items)).
		Msg("Заказ успешно создан")

	return order, nil
}

// GetOrder возвращает заказ по ID.
func (s *orderService) GetOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	log := logger.FromContext(ctx)

	order, err := s.repo.GetByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrOrderNotFound) {
			log.Debug().
				Str("order_id", orderID).
				Msg("Заказ не найден")
			return nil, err
		}
		log.Error().
			Err(err).
			Str("order_id", orderID).
			Msg("Ошибка получения заказа")
		return nil, fmt.Errorf("ошибка получения заказа: %w", err)
	}

	return order, nil
}

// ListOrders возвращает заказы пользователя с пагинацией.
func (s *orderService) ListOrders(ctx context.Context, userID string, status *domain.OrderStatus, page, pageSize int) ([]*domain.Order, int64, error) {
	log := logger.FromContext(ctx)

	// Нормализация параметров пагинации
	page = normalizePage(page)
	pageSize = normalizePageSize(pageSize)

	// Вычисляем offset для SQL
	offset := (page - 1) * pageSize

	orders, total, err := s.repo.ListByUserID(ctx, userID, status, offset, pageSize)
	if err != nil {
		log.Error().
			Err(err).
			Str("user_id", userID).
			Int("page", page).
			Int("page_size", pageSize).
			Msg("Ошибка получения списка заказов")
		return nil, 0, fmt.Errorf("ошибка получения списка заказов: %w", err)
	}

	log.Debug().
		Str("user_id", userID).
		Int("page", page).
		Int("page_size", pageSize).
		Int64("total", total).
		Int("returned", len(orders)).
		Msg("Список заказов получен")

	return orders, total, nil
}

// CancelOrder отменяет заказ.
// failure_reason не заполняется — это поле только для статуса FAILED (Saga).
// ВАЖНО: если сага активна (платёж обрабатывается), отмена запрещена —
// иначе деньги будут списаны, а refund не произойдёт.
func (s *orderService) CancelOrder(ctx context.Context, orderID string) error {
	log := logger.FromContext(ctx)

	// Получаем заказ
	order, err := s.repo.GetByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrOrderNotFound) {
			log.Warn().
				Str("order_id", orderID).
				Msg("Попытка отменить несуществующий заказ")
			return err
		}
		log.Error().
			Err(err).
			Str("order_id", orderID).
			Msg("Ошибка получения заказа для отмены")
		return fmt.Errorf("ошибка получения заказа: %w", err)
	}

	// Проверяем, нет ли активной саги (платёж в процессе обработки)
	if s.orchestrator != nil {
		active, err := s.orchestrator.IsSagaActive(ctx, orderID)
		if err != nil {
			log.Error().Err(err).Str("order_id", orderID).Msg("Ошибка проверки активной саги")
			return fmt.Errorf("ошибка проверки саги: %w", err)
		}
		if active {
			log.Warn().
				Str("order_id", orderID).
				Msg("Попытка отменить заказ с активной сагой — платёж обрабатывается")
			return domain.ErrOrderSagaActive
		}
	}

	// Отменяем заказ (Cancel проверяет CanCancel внутри)
	if err := order.Cancel(); err != nil {
		log.Warn().
			Str("order_id", orderID).
			Str("status", string(order.Status)).
			Msg("Попытка отменить заказ в неподходящем статусе")
		return err
	}

	// Сохраняем изменения с проверкой текущего статуса (защита от TOCTOU race condition)
	if err := s.repo.UpdateStatus(ctx, orderID, domain.OrderStatusPending, order.Status, nil, nil); err != nil {
		log.Error().
			Err(err).
			Str("order_id", orderID).
			Msg("Ошибка сохранения отмены заказа")
		return fmt.Errorf("ошибка сохранения отмены заказа: %w", err)
	}

	log.Info().
		Str("order_id", orderID).
		Msg("Заказ успешно отменён")

	return nil
}

// normalizePage нормализует номер страницы.
// Возвращает минимум 1.
func normalizePage(page int) int {
	if page < 1 {
		return defaultPage
	}
	return page
}

// normalizePageSize нормализует размер страницы.
// Возвращает значение в диапазоне [minPageSize, maxPageSize].
func normalizePageSize(pageSize int) int {
	if pageSize < minPageSize {
		return defaultPageSize
	}
	if pageSize > maxPageSize {
		return maxPageSize
	}
	return pageSize
}
