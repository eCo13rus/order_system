// Package saga реализует обработку Saga команд из Kafka.
// Payment Service слушает saga.commands, обрабатывает платежи и отправляет saga.replies.
package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"example.com/order-system/pkg/kafka"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/payment/internal/service"
)

// =============================================================================
// Типы команд и ответов (совместимы с Order Service)
// =============================================================================

// CommandType — тип команды саги.
type CommandType string

const (
	// CommandProcessPayment — команда на обработку платежа.
	CommandProcessPayment CommandType = "PROCESS_PAYMENT"

	// CommandRefundPayment — команда на возврат платежа.
	CommandRefundPayment CommandType = "REFUND_PAYMENT"
)

// Command — команда из Order Service через Kafka.
type Command struct {
	SagaID    string      `json:"saga_id"`   // ID саги для корреляции
	OrderID   string      `json:"order_id"`  // ID заказа
	Type      CommandType `json:"type"`      // Тип команды
	Amount    int64       `json:"amount"`    // Сумма в минимальных единицах
	Currency  string      `json:"currency"`  // Валюта
	UserID    string      `json:"user_id"`   // ID пользователя
	Timestamp time.Time   `json:"timestamp"` // Время создания команды
}

// ReplyStatus — статус ответа.
type ReplyStatus string

const (
	ReplySuccess ReplyStatus = "SUCCESS"
	ReplyFailed  ReplyStatus = "FAILED"
)

// Reply — ответ для Order Service через Kafka.
type Reply struct {
	SagaID    string      `json:"saga_id"`              // ID саги для корреляции
	OrderID   string      `json:"order_id"`             // ID заказа
	Status    ReplyStatus `json:"status"`               // Результат операции
	PaymentID string      `json:"payment_id,omitempty"` // ID платежа (при успехе)
	Error     string      `json:"error,omitempty"`      // Текст ошибки (при неудаче)
	Timestamp time.Time   `json:"timestamp"`            // Время ответа
}

// =============================================================================
// Интерфейсы для тестируемости
// =============================================================================

// MessageSender — интерфейс для отправки сообщений в Kafka.
// Позволяет мокать producer в unit-тестах.
type MessageSender interface {
	Send(ctx context.Context, topic string, key, value []byte) error
}

// =============================================================================
// Command Handler
// =============================================================================

// CommandHandler обрабатывает Saga команды из Kafka.
type CommandHandler struct {
	consumer       *kafka.Consumer
	sender         MessageSender // Интерфейс для отправки ответов
	paymentService service.PaymentService
}

// NewCommandHandler создаёт новый обработчик команд.
func NewCommandHandler(
	consumer *kafka.Consumer,
	producer *kafka.Producer,
	paymentService service.PaymentService,
) *CommandHandler {
	return &CommandHandler{
		consumer:       consumer,
		sender:         producer, // *kafka.Producer реализует MessageSender
		paymentService: paymentService,
	}
}

// Run запускает обработку команд из Kafka.
// Блокирует выполнение до отмены context.
func (h *CommandHandler) Run(ctx context.Context) error {
	log := logger.Logger()
	log.Info().Msg("Запуск обработчика Saga команд")

	// Используем ConsumeWithRetry для автоматических повторов
	return h.consumer.ConsumeWithRetry(ctx, h.handleMessage, 3)
}

// handleMessage обрабатывает одно сообщение из Kafka.
func (h *CommandHandler) handleMessage(ctx context.Context, msg *kafka.Message) error {
	log := logger.Ctx(ctx)

	// Парсим команду
	var cmd Command
	if err := json.Unmarshal(msg.Value, &cmd); err != nil {
		log.Error().
			Err(err).
			Str("value", string(msg.Value)).
			Msg("Ошибка парсинга команды")
		// Не ретраим — битое сообщение
		return nil
	}

	log.Info().
		Str("saga_id", cmd.SagaID).
		Str("order_id", cmd.OrderID).
		Str("type", string(cmd.Type)).
		Int64("amount", cmd.Amount).
		Msg("Получена Saga команда")

	// Обрабатываем в зависимости от типа
	var reply *Reply
	var err error

	switch cmd.Type {
	case CommandProcessPayment:
		reply, err = h.handleProcessPayment(ctx, &cmd)
	case CommandRefundPayment:
		reply, err = h.handleRefundPayment(ctx, &cmd)
	default:
		log.Warn().
			Str("type", string(cmd.Type)).
			Msg("Неизвестный тип команды")
		// Отправляем ошибку
		reply = &Reply{
			SagaID:    cmd.SagaID,
			OrderID:   cmd.OrderID,
			Status:    ReplyFailed,
			Error:     fmt.Sprintf("неизвестный тип команды: %s", cmd.Type),
			Timestamp: time.Now(),
		}
	}

	if err != nil {
		log.Error().Err(err).Str("saga_id", cmd.SagaID).Msg("Ошибка обработки команды")
		// Формируем ответ с ошибкой
		reply = &Reply{
			SagaID:    cmd.SagaID,
			OrderID:   cmd.OrderID,
			Status:    ReplyFailed,
			Error:     err.Error(),
			Timestamp: time.Now(),
		}
	}

	// Отправляем ответ
	if err := h.sendReply(ctx, reply); err != nil {
		log.Error().Err(err).Str("saga_id", cmd.SagaID).Msg("Ошибка отправки ответа")
		return err // Ретраим
	}

	return nil
}

// handleProcessPayment обрабатывает команду на создание платежа.
func (h *CommandHandler) handleProcessPayment(ctx context.Context, cmd *Command) (*Reply, error) {
	log := logger.Ctx(ctx)

	result, err := h.paymentService.ProcessPayment(ctx, service.ProcessPaymentRequest{
		SagaID:   cmd.SagaID,
		OrderID:  cmd.OrderID,
		UserID:   cmd.UserID,
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
	})

	if err != nil {
		return nil, err
	}

	reply := &Reply{
		SagaID:    cmd.SagaID,
		OrderID:   cmd.OrderID,
		Timestamp: time.Now(),
	}

	if result.Success {
		reply.Status = ReplySuccess
		reply.PaymentID = result.PaymentID
		log.Info().
			Str("saga_id", cmd.SagaID).
			Str("payment_id", result.PaymentID).
			Bool("already_exists", result.AlreadyExists).
			Msg("Платёж успешно обработан")
	} else {
		reply.Status = ReplyFailed
		reply.Error = result.FailureReason
		log.Warn().
			Str("saga_id", cmd.SagaID).
			Str("reason", result.FailureReason).
			Msg("Платёж отклонён")
	}

	return reply, nil
}

// handleRefundPayment обрабатывает команду на возврат платежа (компенсация).
func (h *CommandHandler) handleRefundPayment(ctx context.Context, cmd *Command) (*Reply, error) {
	log := logger.Ctx(ctx)

	log.Info().
		Str("saga_id", cmd.SagaID).
		Msg("Обработка возврата платежа")

	// Находим платёж по saga_id
	payment, err := h.paymentService.GetPaymentBySagaID(ctx, cmd.SagaID)
	if err != nil {
		log.Warn().Err(err).Str("saga_id", cmd.SagaID).Msg("Платёж не найден для возврата")
		// Если платёж не найден — возвращаем успех (нечего возвращать)
		return &Reply{
			SagaID:    cmd.SagaID,
			OrderID:   cmd.OrderID,
			Status:    ReplySuccess,
			Timestamp: time.Now(),
		}, nil
	}

	// Выполняем возврат
	err = h.paymentService.RefundPayment(ctx, service.RefundPaymentRequest{
		PaymentID: payment.ID,
		Reason:    "компенсация саги",
	})
	if err != nil {
		log.Warn().Err(err).Str("payment_id", payment.ID).Msg("Ошибка возврата платежа")
		return &Reply{
			SagaID:    cmd.SagaID,
			OrderID:   cmd.OrderID,
			Status:    ReplyFailed,
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, nil
	}

	log.Info().
		Str("saga_id", cmd.SagaID).
		Str("payment_id", payment.ID).
		Msg("Возврат платежа выполнен")

	return &Reply{
		SagaID:    cmd.SagaID,
		OrderID:   cmd.OrderID,
		PaymentID: payment.ID,
		Status:    ReplySuccess,
		Timestamp: time.Now(),
	}, nil
}

// sendReply отправляет ответ в Kafka.
func (h *CommandHandler) sendReply(ctx context.Context, reply *Reply) error {
	data, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("ошибка сериализации ответа: %w", err)
	}

	// Ключ партиционирования — saga_id (все сообщения одной саги в одной партиции)
	return h.sender.Send(ctx, kafka.TopicSagaReplies, []byte(reply.SagaID), data)
}

// Close закрывает обработчик.
func (h *CommandHandler) Close() error {
	return h.consumer.Close()
}
