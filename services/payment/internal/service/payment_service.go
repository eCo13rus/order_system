// Package service содержит бизнес-логику Payment Service.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/payment/internal/domain"
	"example.com/order-system/services/payment/internal/repository"
)

// =============================================================================
// Конфигурация
// =============================================================================

const (
	// idempotencyKeyPrefix — префикс для ключей идемпотентности в Redis.
	idempotencyKeyPrefix = "payment:idempotency:"

	// idempotencyTTL — время жизни ключа идемпотентности (24 часа).
	idempotencyTTL = 24 * time.Hour
)

// =============================================================================
// Интерфейс сервиса
// =============================================================================

// ProcessPaymentRequest — запрос на обработку платежа.
type ProcessPaymentRequest struct {
	SagaID   string // ID саги для корреляции
	OrderID  string // ID заказа
	UserID   string // ID пользователя
	Amount   int64  // Сумма в минимальных единицах
	Currency string // Валюта
}

// ProcessPaymentResult — результат обработки платежа.
type ProcessPaymentResult struct {
	PaymentID     string // ID созданного платежа
	Success       bool   // Успешность операции
	FailureReason string // Причина ошибки (если !Success)
	AlreadyExists bool   // true если платёж уже существовал (идемпотентность)
}

// RefundPaymentRequest — запрос на возврат платежа.
type RefundPaymentRequest struct {
	PaymentID string // ID платежа
	Reason    string // Причина возврата
}

// PaymentService — интерфейс бизнес-логики платежей.
type PaymentService interface {
	// ProcessPayment обрабатывает платёж для саги.
	// Идемпотентная операция: повторный вызов с тем же saga_id возвращает существующий результат.
	ProcessPayment(ctx context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error)

	// RefundPayment выполняет возврат платежа.
	RefundPayment(ctx context.Context, req RefundPaymentRequest) error

	// GetPayment возвращает платёж по ID.
	GetPayment(ctx context.Context, paymentID string) (*domain.Payment, error)

	// GetPaymentBySagaID возвращает платёж по ID саги.
	GetPaymentBySagaID(ctx context.Context, sagaID string) (*domain.Payment, error)

	// RecoverStuckPayments помечает зависшие PENDING платежи как FAILED.
	// Вызывается периодически для очистки "забытых" платежей.
	RecoverStuckPayments(ctx context.Context) (int, error)
}

// =============================================================================
// Реализация сервиса
// =============================================================================

// paymentService — реализация PaymentService.
type paymentService struct {
	repo  repository.PaymentRepository
	redis *redis.Client
}

// NewPaymentService создаёт новый сервис платежей.
func NewPaymentService(repo repository.PaymentRepository, redisClient *redis.Client) PaymentService {
	return &paymentService{
		repo:  repo,
		redis: redisClient,
	}
}

// ProcessPayment обрабатывает платёж с идемпотентностью.
func (s *paymentService) ProcessPayment(ctx context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error) {
	log := logger.Ctx(ctx)

	// 1. Проверяем идемпотентность через Redis (быстрая проверка)
	idempotencyKey := idempotencyKeyPrefix + req.SagaID

	// Пытаемся установить ключ (SETNX с TTL)
	wasSet, err := s.redis.SetNX(ctx, idempotencyKey, "processing", idempotencyTTL).Result()
	if err != nil {
		log.Error().Err(err).Str("saga_id", req.SagaID).Msg("Ошибка Redis при проверке идемпотентности")
		// При ошибке Redis продолжаем — БД защитит от дубликатов
	}

	// Если ключ уже существует — проверяем платёж в БД
	if !wasSet && err == nil {
		existing, dbErr := s.repo.GetBySagaID(ctx, req.SagaID)
		if dbErr == nil {
			log.Info().
				Str("saga_id", req.SagaID).
				Str("payment_id", existing.ID).
				Msg("Платёж уже существует (идемпотентность)")
			return s.existingPaymentResult(existing), nil
		}
		// Если платёж не найден в БД — возможно он ещё создаётся, продолжаем
	}

	// 2. Создаём платёж в статусе PENDING
	payment := &domain.Payment{
		ID:             uuid.New().String(),
		OrderID:        req.OrderID,
		SagaID:         req.SagaID,
		UserID:         req.UserID,
		Amount:         req.Amount,
		Currency:       req.Currency,
		Status:         domain.PaymentStatusPending,
		PaymentMethod:  "card", // По умолчанию
		IdempotencyKey: req.SagaID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Валидируем
	if err := payment.Validate(); err != nil {
		log.Warn().Err(err).Str("saga_id", req.SagaID).Msg("Невалидные данные платежа")
		return &ProcessPaymentResult{
			Success:       false,
			FailureReason: err.Error(),
		}, nil
	}

	// 3. Сохраняем в БД
	if err := s.repo.Create(ctx, payment); err != nil {
		// Если дубликат — получаем существующий платёж
		if errors.Is(err, domain.ErrDuplicatePayment) {
			existing, dbErr := s.repo.GetBySagaID(ctx, req.SagaID)
			if dbErr == nil {
				log.Info().
					Str("saga_id", req.SagaID).
					Str("payment_id", existing.ID).
					Msg("Платёж уже существует (race condition)")
				return s.existingPaymentResult(existing), nil
			}
		}
		log.Error().Err(err).Str("saga_id", req.SagaID).Msg("Ошибка создания платежа")
		return nil, fmt.Errorf("ошибка создания платежа: %w", err)
	}

	log.Info().
		Str("payment_id", payment.ID).
		Str("saga_id", req.SagaID).
		Int64("amount", req.Amount).
		Msg("Платёж создан, обрабатываем")

	// 4. Симулируем обработку платежа
	// В реальной системе здесь был бы вызов платёжного провайдера
	processErr := s.simulatePaymentProcessing(ctx, payment)

	// 5. Обновляем статус
	if processErr != nil {
		reason := processErr.Error()
		if err := payment.Fail(reason); err != nil {
			log.Error().Err(err).Msg("Ошибка перехода в FAILED")
			return nil, fmt.Errorf("ошибка перехода в FAILED: %w", err)
		}
	} else {
		if err := payment.Complete(); err != nil {
			log.Error().Err(err).Msg("Ошибка перехода в COMPLETED")
			return nil, fmt.Errorf("ошибка перехода в COMPLETED: %w", err)
		}
	}

	// Сохраняем обновлённый статус
	if err := s.repo.Update(ctx, payment); err != nil {
		log.Error().Err(err).Str("payment_id", payment.ID).Msg("Ошибка обновления статуса платежа")
		return nil, fmt.Errorf("ошибка обновления статуса платежа: %w", err)
	}

	// 6. Обновляем Redis — сохраняем ID платежа
	if err := s.redis.Set(ctx, idempotencyKey, payment.ID, idempotencyTTL).Err(); err != nil {
		log.Warn().Err(err).Msg("Ошибка обновления ключа идемпотентности в Redis")
	}

	log.Info().
		Str("payment_id", payment.ID).
		Str("status", string(payment.Status)).
		Bool("success", payment.Status == domain.PaymentStatusCompleted).
		Msg("Платёж обработан")

	return &ProcessPaymentResult{
		PaymentID:     payment.ID,
		Success:       payment.Status == domain.PaymentStatusCompleted,
		FailureReason: s.getFailureReason(payment),
		AlreadyExists: false,
	}, nil
}

// RefundPayment выполняет возврат платежа.
func (s *paymentService) RefundPayment(ctx context.Context, req RefundPaymentRequest) error {
	log := logger.Ctx(ctx)

	// Получаем платёж
	payment, err := s.repo.GetByID(ctx, req.PaymentID)
	if err != nil {
		return err
	}

	// Выполняем возврат
	refundID := uuid.New().String()
	if err := payment.Refund(refundID, req.Reason); err != nil {
		log.Warn().
			Err(err).
			Str("payment_id", req.PaymentID).
			Str("status", string(payment.Status)).
			Msg("Невозможно выполнить возврат")
		return err
	}

	// Сохраняем
	if err := s.repo.Update(ctx, payment); err != nil {
		return fmt.Errorf("ошибка обновления платежа: %w", err)
	}

	log.Info().
		Str("payment_id", req.PaymentID).
		Str("refund_id", refundID).
		Msg("Возврат платежа выполнен")

	return nil
}

// GetPayment возвращает платёж по ID.
func (s *paymentService) GetPayment(ctx context.Context, paymentID string) (*domain.Payment, error) {
	return s.repo.GetByID(ctx, paymentID)
}

// GetPaymentBySagaID возвращает платёж по ID саги.
func (s *paymentService) GetPaymentBySagaID(ctx context.Context, sagaID string) (*domain.Payment, error) {
	return s.repo.GetBySagaID(ctx, sagaID)
}

// RecoverStuckPayments помечает зависшие PENDING платежи как FAILED.
// Платёж считается зависшим, если он в PENDING более 5 минут.
func (s *paymentService) RecoverStuckPayments(ctx context.Context) (int, error) {
	log := logger.Ctx(ctx)

	// Ищем платежи в PENDING старше 5 минут (максимум 100 за раз)
	stuckPayments, err := s.repo.GetStuckPending(ctx, 5*time.Minute, 100)
	if err != nil {
		return 0, fmt.Errorf("ошибка получения зависших платежей: %w", err)
	}

	if len(stuckPayments) == 0 {
		return 0, nil
	}

	recovered := 0
	for _, payment := range stuckPayments {
		reason := "таймаут обработки платежа"
		if err := payment.Fail(reason); err != nil {
			log.Warn().Err(err).Str("payment_id", payment.ID).Msg("Не удалось пометить платёж как FAILED")
			continue
		}

		if err := s.repo.Update(ctx, payment); err != nil {
			log.Warn().Err(err).Str("payment_id", payment.ID).Msg("Ошибка обновления зависшего платежа")
			continue
		}

		log.Info().
			Str("payment_id", payment.ID).
			Str("saga_id", payment.SagaID).
			Msg("Зависший платёж помечен как FAILED")
		recovered++
	}

	if recovered > 0 {
		log.Info().Int("count", recovered).Msg("Восстановлено зависших платежей")
	}

	return recovered, nil
}

// =============================================================================
// Вспомогательные методы
// =============================================================================

// getFailureReason возвращает причину ошибки или пустую строку.
func (s *paymentService) getFailureReason(p *domain.Payment) string {
	if p.FailureReason != nil {
		return *p.FailureReason
	}
	return ""
}

// existingPaymentResult формирует результат для уже существующего платежа (идемпотентность).
func (s *paymentService) existingPaymentResult(p *domain.Payment) *ProcessPaymentResult {
	return &ProcessPaymentResult{
		PaymentID:     p.ID,
		Success:       p.Status == domain.PaymentStatusCompleted,
		FailureReason: s.getFailureReason(p),
		AlreadyExists: true,
	}
}

// simulatePaymentProcessing симулирует обработку платежа.
// В реальной системе здесь был бы вызов платёжного провайдера (Stripe, YooKassa и т.д.).
// Возвращает nil при успехе, ошибку при отклонении.
func (s *paymentService) simulatePaymentProcessing(ctx context.Context, payment *domain.Payment) error {
	log := logger.Ctx(ctx)

	// Симуляция: отклоняем платежи с суммой, кратной 666 (для тестирования failure flow)
	if payment.Amount%666 == 0 && payment.Amount > 0 {
		log.Warn().
			Str("payment_id", payment.ID).
			Int64("amount", payment.Amount).
			Msg("Платёж отклонён (симуляция: сумма кратна 666)")
		return domain.ErrInsufficientFunds
	}

	// Всё остальное — успех
	log.Debug().
		Str("payment_id", payment.ID).
		Int64("amount", payment.Amount).
		Msg("Платёж одобрен (симуляция)")

	return nil
}
