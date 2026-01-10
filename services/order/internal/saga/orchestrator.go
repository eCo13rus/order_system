package saga

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/order/internal/domain"
	"example.com/order-system/services/order/internal/repository"
)

// =============================================================================
// Orchestrator — координатор Saga-транзакций
// =============================================================================

// Orchestrator координирует распределённую транзакцию создания заказа.
// Реализует паттерн Saga Orchestration:
// 1. Создаёт заказ в статусе PENDING
// 2. Отправляет команду ProcessPayment в Payment Service
// 3. Обрабатывает ответ: подтверждает или откатывает заказ
type Orchestrator interface {
	// CreateOrderWithSaga атомарно создаёт заказ, сагу и команду ProcessPayment.
	// КРИТИЧНО: решает проблему dual write — всё в одной транзакции.
	// Если любая часть падает — откатывается ВСЁ, клиент получает ошибку.
	CreateOrderWithSaga(ctx context.Context, order *domain.Order) error

	// StartSaga инициирует сагу для существующего заказа.
	// DEPRECATED: используй CreateOrderWithSaga для новых заказов.
	// Оставлен для обратной совместимости и recovery job.
	StartSaga(ctx context.Context, order *domain.Order) error

	// HandlePaymentReply обрабатывает ответ от Payment Service.
	// При успехе — подтверждает заказ, при ошибке — запускает компенсацию.
	HandlePaymentReply(ctx context.Context, reply *Reply) error

	// CompensateSaga выполняет откат саги при ошибке.
	// Помечает заказ как FAILED с указанием причины.
	CompensateSaga(ctx context.Context, sagaID string, reason string) error
}

// orchestrator — реализация Orchestrator.
type orchestrator struct {
	sagaRepo  SagaRepository
	orderRepo repository.OrderRepository
}

// NewOrchestrator создаёт новый координатор саг.
// outboxRepo не требуется — все outbox записи создаются атомарно через SagaRepository.
func NewOrchestrator(
	sagaRepo SagaRepository,
	orderRepo repository.OrderRepository,
) Orchestrator {
	return &orchestrator{
		sagaRepo:  sagaRepo,
		orderRepo: orderRepo,
	}
}

// CreateOrderWithSaga атомарно создаёт заказ, сагу и команду ProcessPayment.
// КРИТИЧНО: решает проблему dual write — order+saga+outbox в ОДНОЙ транзакции.
// Если что-то падает — откатывается ВСЁ, клиент получает ошибку и может повторить.
func (o *orchestrator) CreateOrderWithSaga(ctx context.Context, order *domain.Order) error {
	log := logger.FromContext(ctx)

	// Генерируем ID для саги и записи outbox
	sagaID := uuid.New().String()
	outboxID := uuid.New().String()
	now := time.Now()

	// Создаём сагу СРАЗУ в состоянии PAYMENT_PENDING
	saga := &Saga{
		ID:      sagaID,
		OrderID: order.ID,
		Status:  StatusPaymentPending,
		StepData: &StepData{
			Amount:   order.TotalAmount.Amount,
			Currency: order.TotalAmount.Currency,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Формируем команду ProcessPayment
	cmd := &Command{
		SagaID:    sagaID,
		OrderID:   order.ID,
		Type:      CommandProcessPayment,
		Amount:    order.TotalAmount.Amount,
		Currency:  order.TotalAmount.Currency,
		Timestamp: now,
	}

	// Собираем headers для трассировки
	headers := map[string]string{
		kafka.HeaderTraceID:       kafka.TraceIDFromContext(ctx),
		kafka.HeaderCorrelationID: kafka.CorrelationIDFromContext(ctx),
	}

	// Создаём запись outbox
	outbox, err := NewOutbox(outboxID, order.ID, kafka.TopicSagaCommands, cmd, headers)
	if err != nil {
		log.Error().Err(err).Str("order_id", order.ID).Msg("Ошибка создания outbox записи")
		return fmt.Errorf("ошибка создания outbox: %w", err)
	}

	// АТОМАРНО создаём order + saga + outbox в ОДНОЙ транзакции
	// Если что-то падает — откатывается ВСЁ
	if err := o.sagaRepo.CreateOrderWithSagaAndOutbox(ctx, order, saga, outbox); err != nil {
		log.Error().Err(err).Str("order_id", order.ID).Msg("Ошибка атомарного создания заказа с сагой")
		return fmt.Errorf("ошибка создания заказа: %w", err)
	}

	log.Info().
		Str("saga_id", sagaID).
		Str("order_id", order.ID).
		Int64("amount", order.TotalAmount.Amount).
		Str("currency", order.TotalAmount.Currency).
		Msg("Заказ и сага созданы атомарно")

	return nil
}

// StartSaga инициирует сагу для существующего заказа.
// DEPRECATED: используй CreateOrderWithSaga для новых заказов.
// Оставлен для recovery job (заказы без саги).
func (o *orchestrator) StartSaga(ctx context.Context, order *domain.Order) error {
	log := logger.FromContext(ctx)

	// Генерируем ID для саги и записи outbox
	sagaID := uuid.New().String()
	outboxID := uuid.New().String()
	now := time.Now()

	// Создаём сагу СРАЗУ в состоянии PAYMENT_PENDING
	// Это избегает race condition: если транзакция прошла — сага готова принять ответ
	saga := &Saga{
		ID:      sagaID,
		OrderID: order.ID,
		Status:  StatusPaymentPending, // Сразу в PAYMENT_PENDING!
		StepData: &StepData{
			Amount:   order.TotalAmount.Amount,
			Currency: order.TotalAmount.Currency,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Формируем команду ProcessPayment
	cmd := &Command{
		SagaID:    sagaID,
		OrderID:   order.ID,
		Type:      CommandProcessPayment,
		Amount:    order.TotalAmount.Amount,
		Currency:  order.TotalAmount.Currency,
		Timestamp: now,
	}

	// Собираем headers для трассировки
	headers := map[string]string{
		kafka.HeaderTraceID:       kafka.TraceIDFromContext(ctx),
		kafka.HeaderCorrelationID: kafka.CorrelationIDFromContext(ctx),
	}

	// Создаём запись outbox
	outbox, err := NewOutbox(outboxID, order.ID, kafka.TopicSagaCommands, cmd, headers)
	if err != nil {
		log.Error().Err(err).Str("order_id", order.ID).Msg("Ошибка создания outbox записи")
		return fmt.Errorf("ошибка создания outbox: %w", err)
	}

	// Атомарно создаём сагу в PAYMENT_PENDING и outbox запись
	// Одна транзакция = консистентность данных
	if err := o.sagaRepo.CreateWithOutbox(ctx, saga, outbox); err != nil {
		log.Error().Err(err).Str("order_id", order.ID).Msg("Ошибка создания саги")
		return fmt.Errorf("ошибка создания саги: %w", err)
	}

	log.Info().
		Str("saga_id", sagaID).
		Str("order_id", order.ID).
		Int64("amount", order.TotalAmount.Amount).
		Str("currency", order.TotalAmount.Currency).
		Msg("Сага запущена, команда ProcessPayment добавлена в outbox")

	return nil
}

// HandlePaymentReply обрабатывает ответ от Payment Service.
func (o *orchestrator) HandlePaymentReply(ctx context.Context, reply *Reply) error {
	log := logger.FromContext(ctx)

	// Получаем сагу по ID
	saga, err := o.sagaRepo.GetByID(ctx, reply.SagaID)
	if err != nil {
		log.Error().Err(err).Str("saga_id", reply.SagaID).Msg("Сага не найдена")
		return fmt.Errorf("сага не найдена: %w", err)
	}

	// Проверяем, что сага в ожидаемом состоянии
	if saga.Status != StatusPaymentPending {
		log.Warn().
			Str("saga_id", reply.SagaID).
			Str("current_status", string(saga.Status)).
			Msg("Сага не в состоянии PAYMENT_PENDING, пропускаем ответ")
		return nil
	}

	if reply.IsSuccess() {
		return o.handlePaymentSuccess(ctx, saga, reply)
	}
	return o.handlePaymentFailure(ctx, saga, reply)
}

// handlePaymentSuccess обрабатывает успешную оплату.
func (o *orchestrator) handlePaymentSuccess(ctx context.Context, saga *Saga, reply *Reply) error {
	log := logger.FromContext(ctx)

	// Переводим сагу в COMPLETED
	if err := saga.Complete(reply.PaymentID); err != nil {
		log.Error().Err(err).Str("saga_id", saga.ID).Msg("Ошибка завершения саги")
		return fmt.Errorf("ошибка завершения саги: %w", err)
	}

	// Получаем заказ
	order, err := o.orderRepo.GetByID(ctx, saga.OrderID)
	if err != nil {
		log.Error().Err(err).Str("order_id", saga.OrderID).Msg("Ошибка получения заказа")
		return fmt.Errorf("ошибка получения заказа: %w", err)
	}

	// Подтверждаем заказ через доменный метод (проверяет статус)
	if err := order.Confirm(reply.PaymentID); err != nil {
		log.Error().Err(err).
			Str("order_id", saga.OrderID).
			Str("status", string(order.Status)).
			Msg("Невозможно подтвердить заказ — неподходящий статус")
		return fmt.Errorf("ошибка подтверждения заказа: %w", err)
	}

	// Атомарно обновляем сагу и заказ в одной транзакции
	if err := o.sagaRepo.UpdateWithOrder(ctx, saga, order.ID, order.Status, order.PaymentID, order.FailureReason); err != nil {
		log.Error().Err(err).Str("saga_id", saga.ID).Msg("Ошибка обновления саги и заказа")
		return fmt.Errorf("ошибка обновления: %w", err)
	}

	log.Info().
		Str("saga_id", saga.ID).
		Str("order_id", saga.OrderID).
		Str("payment_id", reply.PaymentID).
		Msg("Сага завершена успешно, заказ подтверждён")

	return nil
}

// handlePaymentFailure обрабатывает неудачную оплату и запускает компенсацию.
func (o *orchestrator) handlePaymentFailure(ctx context.Context, saga *Saga, reply *Reply) error {
	log := logger.FromContext(ctx)

	log.Warn().
		Str("saga_id", saga.ID).
		Str("order_id", saga.OrderID).
		Str("error", reply.Error).
		Str("payment_id", reply.PaymentID).
		Msg("Платёж отклонён, запускаем компенсацию")

	// Если Payment Service вернул PaymentID — значит платёж был создан и нужен refund
	// Если PaymentID пустой — платёж не прошёл, refund не нужен
	return o.compensateSagaWithRefund(ctx, saga, reply.Error, reply.PaymentID)
}

// CompensateSaga выполняет откат саги (публичный метод без refund).
func (o *orchestrator) CompensateSaga(ctx context.Context, sagaID string, reason string) error {
	saga, err := o.sagaRepo.GetByID(ctx, sagaID)
	if err != nil {
		return fmt.Errorf("сага не найдена: %w", err)
	}
	return o.compensateSagaWithRefund(ctx, saga, reason, "")
}

// compensateSagaWithRefund выполняет откат саги с опциональным refund.
// Если paymentID указан — создаёт команду RefundPayment в outbox.
// КРИТИЧНО: Всё выполняется в одной атомарной транзакции для предотвращения дублирования refund при retry.
func (o *orchestrator) compensateSagaWithRefund(ctx context.Context, saga *Saga, reason, paymentID string) error {
	log := logger.FromContext(ctx)

	// 1. Переводим сагу в FAILED (через COMPENSATING)
	if err := saga.Fail(reason); err != nil {
		log.Error().Err(err).Str("saga_id", saga.ID).Msg("Ошибка перехода в FAILED")
		return fmt.Errorf("ошибка перехода состояния: %w", err)
	}

	// 2. Получаем заказ и переводим в FAILED
	order, err := o.orderRepo.GetByID(ctx, saga.OrderID)
	if err != nil {
		log.Error().Err(err).Str("order_id", saga.OrderID).Msg("Ошибка получения заказа")
		return fmt.Errorf("ошибка получения заказа: %w", err)
	}

	if err := order.Fail(reason); err != nil {
		log.Error().Err(err).
			Str("order_id", saga.OrderID).
			Str("status", string(order.Status)).
			Msg("Невозможно пометить заказ как failed — неподходящий статус")
		return fmt.Errorf("ошибка пометки заказа: %w", err)
	}

	// 3. Подготавливаем refund outbox (если нужен)
	var refundOutbox *Outbox
	if paymentID != "" {
		outbox, err := o.buildRefundOutbox(ctx, saga, paymentID)
		if err != nil {
			log.Error().Err(err).
				Str("saga_id", saga.ID).
				Str("payment_id", paymentID).
				Msg("Ошибка создания RefundPayment outbox, продолжаем без refund")
			// Не прерываем — refund можно обработать вручную
		} else {
			refundOutbox = outbox
		}
	}

	// 4. АТОМАРНО обновляем сагу, заказ и создаём refund outbox
	// Это предотвращает дублирование refund при retry — либо всё записывается, либо ничего
	if err := o.sagaRepo.UpdateWithOrderAndOutbox(ctx, saga, order.ID, order.Status, order.PaymentID, order.FailureReason, refundOutbox); err != nil {
		log.Error().Err(err).Str("saga_id", saga.ID).Msg("Ошибка атомарного обновления")
		return fmt.Errorf("ошибка обновления: %w", err)
	}

	log.Info().
		Str("saga_id", saga.ID).
		Str("order_id", saga.OrderID).
		Str("reason", reason).
		Bool("refund_created", refundOutbox != nil).
		Msg("Компенсация выполнена, заказ помечен как FAILED")

	return nil
}

// buildRefundOutbox создаёт объект Outbox для RefundPayment команды.
// НЕ сохраняет в БД — сохранение происходит в атомарной транзакции UpdateWithOrderAndOutbox.
func (o *orchestrator) buildRefundOutbox(ctx context.Context, saga *Saga, paymentID string) (*Outbox, error) {
	// Защита от nil StepData — критически важно для избежания panic
	if saga.StepData == nil {
		return nil, fmt.Errorf("saga %s: StepData is nil, невозможно создать refund команду", saga.ID)
	}

	outboxID := uuid.New().String()
	now := time.Now()

	// Формируем команду RefundPayment
	cmd := &Command{
		SagaID:    saga.ID,
		OrderID:   saga.OrderID,
		Type:      CommandRefundPayment,
		Amount:    saga.StepData.Amount,
		Currency:  saga.StepData.Currency,
		Timestamp: now,
	}

	// Собираем headers для трассировки
	headers := map[string]string{
		kafka.HeaderTraceID:       kafka.TraceIDFromContext(ctx),
		kafka.HeaderCorrelationID: kafka.CorrelationIDFromContext(ctx),
		"payment_id":              paymentID, // Передаём ID платежа для refund
	}

	// Создаём запись outbox (без сохранения)
	outbox, err := NewOutbox(outboxID, saga.OrderID, kafka.TopicSagaCommands, cmd, headers)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания outbox для refund: %w", err)
	}

	return outbox, nil
}
