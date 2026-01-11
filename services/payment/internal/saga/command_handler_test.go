package saga

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/order-system/pkg/kafka"
	"example.com/order-system/services/payment/internal/domain"
	"example.com/order-system/services/payment/internal/service"
)

// =============================================================================
// Моки
// =============================================================================

// mockSender — мок для MessageSender (отправка в Kafka).
type mockSender struct {
	lastTopic string
	lastKey   []byte
	lastValue []byte
	lastReply *Reply
	sendErr   error
}

func (m *mockSender) Send(ctx context.Context, topic string, key, value []byte) error {
	m.lastTopic = topic
	m.lastKey = key
	m.lastValue = value

	// Парсим Reply для проверки в тестах
	if len(value) > 0 {
		m.lastReply = &Reply{}
		_ = json.Unmarshal(value, m.lastReply)
	}

	return m.sendErr
}

// mockPaymentService — мок для тестирования saga handler.
type mockPaymentService struct {
	// ProcessPayment
	processResult *service.ProcessPaymentResult
	processErr    error

	// RefundPayment
	refundErr error

	// GetPaymentBySagaID
	payment       *domain.Payment
	getPaymentErr error
}

func (m *mockPaymentService) ProcessPayment(ctx context.Context, req service.ProcessPaymentRequest) (*service.ProcessPaymentResult, error) {
	if m.processErr != nil {
		return nil, m.processErr
	}
	return m.processResult, nil
}

func (m *mockPaymentService) RefundPayment(ctx context.Context, req service.RefundPaymentRequest) error {
	return m.refundErr
}

func (m *mockPaymentService) GetPayment(ctx context.Context, paymentID string) (*domain.Payment, error) {
	return m.payment, m.getPaymentErr
}

func (m *mockPaymentService) GetPaymentBySagaID(ctx context.Context, sagaID string) (*domain.Payment, error) {
	if m.getPaymentErr != nil {
		return nil, m.getPaymentErr
	}
	return m.payment, nil
}

func (m *mockPaymentService) RecoverStuckPayments(ctx context.Context) (int, error) {
	return 0, nil // Не используется в saga тестах
}

// =============================================================================
// Тесты handleProcessPayment
// =============================================================================

func TestCommandHandler_HandleProcessPayment_Success(t *testing.T) {
	// Arrange
	mock := &mockPaymentService{
		processResult: &service.ProcessPaymentResult{
			PaymentID:     "payment-123",
			Success:       true,
			AlreadyExists: false,
		},
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:    "saga-123",
		OrderID:   "order-123",
		UserID:    "user-123",
		Type:      CommandProcessPayment,
		Amount:    10000,
		Currency:  "RUB",
		Timestamp: time.Now(),
	}

	// Act
	reply, err := handler.handleProcessPayment(context.Background(), cmd)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, reply)
	assert.Equal(t, ReplySuccess, reply.Status)
	assert.Equal(t, "saga-123", reply.SagaID)
	assert.Equal(t, "order-123", reply.OrderID)
	assert.Equal(t, "payment-123", reply.PaymentID)
	assert.Empty(t, reply.Error)
}

func TestCommandHandler_HandleProcessPayment_PaymentFailed(t *testing.T) {
	// Arrange — платёж отклонён (недостаточно средств)
	mock := &mockPaymentService{
		processResult: &service.ProcessPaymentResult{
			PaymentID:     "payment-456",
			Success:       false,
			FailureReason: "недостаточно средств для оплаты",
		},
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:   "saga-456",
		OrderID:  "order-456",
		UserID:   "user-456",
		Type:     CommandProcessPayment,
		Amount:   666, // Кратна 666 — симуляция отказа
		Currency: "RUB",
	}

	// Act
	reply, err := handler.handleProcessPayment(context.Background(), cmd)

	// Assert
	require.NoError(t, err) // Ошибки нет — это бизнес-результат
	require.NotNil(t, reply)
	assert.Equal(t, ReplyFailed, reply.Status)
	assert.Equal(t, "недостаточно средств для оплаты", reply.Error)
}

func TestCommandHandler_HandleProcessPayment_ServiceError(t *testing.T) {
	// Arrange — ошибка сервиса (БД недоступна)
	mock := &mockPaymentService{
		processErr: errors.New("connection refused"),
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:  "saga-err",
		OrderID: "order-err",
		UserID:  "user-err",
		Type:    CommandProcessPayment,
		Amount:  1000,
	}

	// Act
	reply, err := handler.handleProcessPayment(context.Background(), cmd)

	// Assert
	require.Error(t, err)
	assert.Nil(t, reply)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestCommandHandler_HandleProcessPayment_Idempotency(t *testing.T) {
	// Arrange — повторный запрос, платёж уже существует
	mock := &mockPaymentService{
		processResult: &service.ProcessPaymentResult{
			PaymentID:     "payment-existing",
			Success:       true,
			AlreadyExists: true, // Идемпотентность сработала
		},
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:  "saga-idempotent",
		OrderID: "order-idempotent",
		UserID:  "user-idempotent",
		Type:    CommandProcessPayment,
		Amount:  5000,
	}

	// Act
	reply, err := handler.handleProcessPayment(context.Background(), cmd)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, reply)
	assert.Equal(t, ReplySuccess, reply.Status)
	assert.Equal(t, "payment-existing", reply.PaymentID)
}

// =============================================================================
// Тесты handleRefundPayment
// =============================================================================

func TestCommandHandler_HandleRefundPayment_Success(t *testing.T) {
	// Arrange — платёж найден и успешно возвращён
	mock := &mockPaymentService{
		payment: &domain.Payment{
			ID:      "payment-refund-123",
			SagaID:  "saga-refund",
			OrderID: "order-refund",
			Status:  domain.PaymentStatusCompleted,
		},
		refundErr: nil,
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:  "saga-refund",
		OrderID: "order-refund",
		Type:    CommandRefundPayment,
	}

	// Act
	reply, err := handler.handleRefundPayment(context.Background(), cmd)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, reply)
	assert.Equal(t, ReplySuccess, reply.Status)
	assert.Equal(t, "payment-refund-123", reply.PaymentID)
	assert.Empty(t, reply.Error)
}

func TestCommandHandler_HandleRefundPayment_NotFound(t *testing.T) {
	// Arrange — платёж не найден (нечего возвращать — это SUCCESS для саги)
	mock := &mockPaymentService{
		getPaymentErr: domain.ErrPaymentNotFound,
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:  "saga-not-found",
		OrderID: "order-not-found",
		Type:    CommandRefundPayment,
	}

	// Act
	reply, err := handler.handleRefundPayment(context.Background(), cmd)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, reply)
	assert.Equal(t, ReplySuccess, reply.Status) // Нечего возвращать — успех
	assert.Empty(t, reply.PaymentID)
}

func TestCommandHandler_HandleRefundPayment_RefundError(t *testing.T) {
	// Arrange — платёж найден, но возврат не удался (например, уже FAILED)
	mock := &mockPaymentService{
		payment: &domain.Payment{
			ID:     "payment-failed",
			SagaID: "saga-failed",
			Status: domain.PaymentStatusFailed,
		},
		refundErr: domain.ErrInvalidTransition,
	}

	handler := &CommandHandler{paymentService: mock}
	cmd := &Command{
		SagaID:  "saga-failed",
		OrderID: "order-failed",
		Type:    CommandRefundPayment,
	}

	// Act
	reply, err := handler.handleRefundPayment(context.Background(), cmd)

	// Assert
	require.NoError(t, err) // Не возвращаем error — формируем Reply с FAILED
	require.NotNil(t, reply)
	assert.Equal(t, ReplyFailed, reply.Status)
	assert.Contains(t, reply.Error, "недопустимый переход")
}

// =============================================================================
// Тесты парсинга команд
// =============================================================================

func TestCommand_ParseUnknownType(t *testing.T) {
	// Arrange — неизвестный тип команды
	cmdJSON := `{"saga_id":"saga-unknown","order_id":"order-unknown","type":"UNKNOWN_COMMAND","amount":1000}`

	// Act
	var cmd Command
	err := json.Unmarshal([]byte(cmdJSON), &cmd)

	// Assert — парсинг успешен, но тип неизвестный
	require.NoError(t, err)
	assert.Equal(t, CommandType("UNKNOWN_COMMAND"), cmd.Type)
	assert.Equal(t, "saga-unknown", cmd.SagaID)
}

func TestCommand_ParseInvalidJSON(t *testing.T) {
	// Arrange — битый JSON
	invalidJSON := `{"saga_id": broken json`

	// Act
	var cmd Command
	err := json.Unmarshal([]byte(invalidJSON), &cmd)

	// Assert — парсинг должен упасть
	require.Error(t, err)
}

func TestCommand_ParseValidCommand(t *testing.T) {
	// Arrange
	cmdJSON := `{
		"saga_id": "saga-valid",
		"order_id": "order-valid",
		"type": "PROCESS_PAYMENT",
		"amount": 5000,
		"currency": "RUB",
		"user_id": "user-valid"
	}`

	// Act
	var cmd Command
	err := json.Unmarshal([]byte(cmdJSON), &cmd)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, CommandProcessPayment, cmd.Type)
	assert.Equal(t, "saga-valid", cmd.SagaID)
	assert.Equal(t, "order-valid", cmd.OrderID)
	assert.Equal(t, "user-valid", cmd.UserID)
	assert.Equal(t, int64(5000), cmd.Amount)
	assert.Equal(t, "RUB", cmd.Currency)
}

// =============================================================================
// Тесты Reply сериализации
// =============================================================================

func TestReply_Serialization(t *testing.T) {
	// Arrange
	reply := &Reply{
		SagaID:    "saga-reply",
		OrderID:   "order-reply",
		Status:    ReplySuccess,
		PaymentID: "payment-reply",
		Timestamp: time.Now(),
	}

	// Act
	data, err := json.Marshal(reply)
	require.NoError(t, err)

	var parsed Reply
	err = json.Unmarshal(data, &parsed)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, reply.SagaID, parsed.SagaID)
	assert.Equal(t, reply.Status, parsed.Status)
	assert.Equal(t, reply.PaymentID, parsed.PaymentID)
}

// =============================================================================
// Тесты handleMessage (полный flow с отправкой reply)
// =============================================================================

func TestCommandHandler_HandleMessage_ProcessPayment_Success(t *testing.T) {
	// Arrange
	sender := &mockSender{}
	paymentSvc := &mockPaymentService{
		processResult: &service.ProcessPaymentResult{
			PaymentID: "payment-msg-123",
			Success:   true,
		},
	}

	handler := &CommandHandler{
		sender:         sender,
		paymentService: paymentSvc,
	}

	cmdJSON := `{"saga_id":"saga-msg","order_id":"order-msg","user_id":"user-msg","type":"PROCESS_PAYMENT","amount":5000,"currency":"RUB"}`
	msg := &kafka.Message{Value: []byte(cmdJSON)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, sender.lastReply)
	assert.Equal(t, ReplySuccess, sender.lastReply.Status)
	assert.Equal(t, "saga-msg", sender.lastReply.SagaID)
	assert.Equal(t, "payment-msg-123", sender.lastReply.PaymentID)
	assert.Equal(t, kafka.TopicSagaReplies, sender.lastTopic)
}

func TestCommandHandler_HandleMessage_ProcessPayment_Failed(t *testing.T) {
	// Arrange
	sender := &mockSender{}
	paymentSvc := &mockPaymentService{
		processResult: &service.ProcessPaymentResult{
			PaymentID:     "payment-fail",
			Success:       false,
			FailureReason: "недостаточно средств",
		},
	}

	handler := &CommandHandler{
		sender:         sender,
		paymentService: paymentSvc,
	}

	cmdJSON := `{"saga_id":"saga-fail","order_id":"order-fail","type":"PROCESS_PAYMENT","amount":666}`
	msg := &kafka.Message{Value: []byte(cmdJSON)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, sender.lastReply)
	assert.Equal(t, ReplyFailed, sender.lastReply.Status)
	assert.Equal(t, "недостаточно средств", sender.lastReply.Error)
}

func TestCommandHandler_HandleMessage_RefundPayment_Success(t *testing.T) {
	// Arrange
	sender := &mockSender{}
	paymentSvc := &mockPaymentService{
		payment: &domain.Payment{
			ID:     "payment-refund",
			SagaID: "saga-refund-msg",
			Status: domain.PaymentStatusCompleted,
		},
	}

	handler := &CommandHandler{
		sender:         sender,
		paymentService: paymentSvc,
	}

	cmdJSON := `{"saga_id":"saga-refund-msg","order_id":"order-refund","type":"REFUND_PAYMENT"}`
	msg := &kafka.Message{Value: []byte(cmdJSON)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, sender.lastReply)
	assert.Equal(t, ReplySuccess, sender.lastReply.Status)
	assert.Equal(t, "payment-refund", sender.lastReply.PaymentID)
}

func TestCommandHandler_HandleMessage_UnknownCommand(t *testing.T) {
	// Arrange
	sender := &mockSender{}
	handler := &CommandHandler{
		sender:         sender,
		paymentService: &mockPaymentService{},
	}

	cmdJSON := `{"saga_id":"saga-unknown","order_id":"order-unknown","type":"UNKNOWN_TYPE","amount":1000}`
	msg := &kafka.Message{Value: []byte(cmdJSON)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, sender.lastReply)
	assert.Equal(t, ReplyFailed, sender.lastReply.Status)
	assert.Contains(t, sender.lastReply.Error, "неизвестный тип команды")
}

func TestCommandHandler_HandleMessage_InvalidJSON(t *testing.T) {
	// Arrange
	sender := &mockSender{}
	handler := &CommandHandler{
		sender:         sender,
		paymentService: &mockPaymentService{},
	}

	msg := &kafka.Message{Value: []byte(`{invalid json`)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert — битый JSON не ретраится, возвращаем nil
	require.NoError(t, err)
	assert.Nil(t, sender.lastReply) // Reply не отправлен
}

func TestCommandHandler_HandleMessage_ServiceError(t *testing.T) {
	// Arrange — ошибка сервиса
	sender := &mockSender{}
	paymentSvc := &mockPaymentService{
		processErr: errors.New("database connection failed"),
	}

	handler := &CommandHandler{
		sender:         sender,
		paymentService: paymentSvc,
	}

	cmdJSON := `{"saga_id":"saga-db-err","order_id":"order-db-err","type":"PROCESS_PAYMENT","amount":1000}`
	msg := &kafka.Message{Value: []byte(cmdJSON)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert — при ошибке сервиса формируется Reply с FAILED
	require.NoError(t, err)
	require.NotNil(t, sender.lastReply)
	assert.Equal(t, ReplyFailed, sender.lastReply.Status)
	assert.Contains(t, sender.lastReply.Error, "database connection failed")
}

func TestCommandHandler_HandleMessage_SendReplyError(t *testing.T) {
	// Arrange — ошибка отправки reply
	sender := &mockSender{
		sendErr: errors.New("kafka unavailable"),
	}
	paymentSvc := &mockPaymentService{
		processResult: &service.ProcessPaymentResult{
			PaymentID: "payment-ok",
			Success:   true,
		},
	}

	handler := &CommandHandler{
		sender:         sender,
		paymentService: paymentSvc,
	}

	cmdJSON := `{"saga_id":"saga-send-err","order_id":"order-send-err","type":"PROCESS_PAYMENT","amount":1000}`
	msg := &kafka.Message{Value: []byte(cmdJSON)}

	// Act
	err := handler.handleMessage(context.Background(), msg)

	// Assert — ошибка отправки возвращается для retry
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kafka unavailable")
}

// =============================================================================
// Тесты sendReply
// =============================================================================

func TestCommandHandler_SendReply(t *testing.T) {
	// Arrange
	sender := &mockSender{}
	handler := &CommandHandler{sender: sender}

	reply := &Reply{
		SagaID:    "saga-send",
		OrderID:   "order-send",
		Status:    ReplySuccess,
		PaymentID: "payment-send",
		Timestamp: time.Now(),
	}

	// Act
	err := handler.sendReply(context.Background(), reply)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, kafka.TopicSagaReplies, sender.lastTopic)
	assert.Equal(t, []byte("saga-send"), sender.lastKey)
	assert.Equal(t, ReplySuccess, sender.lastReply.Status)
}
