//go:build integration

// Package saga — интеграционные тесты SagaRepository и OutboxRepository.
// Требует: MySQL (настройки из .env).
// Запуск: go test -tags=integration -v ./services/order/internal/saga/...
package saga

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"example.com/order-system/services/order/internal/domain"
)

// =============================================================================
// Инфраструктура тестов
// =============================================================================

var testDB *gorm.DB

// mysqlDSN собирает DSN из переменных .env
func mysqlDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		os.Getenv("MYSQL_USER"), os.Getenv("MYSQL_PASSWORD"),
		os.Getenv("MYSQL_HOST"), os.Getenv("MYSQL_PORT"), os.Getenv("MYSQL_DATABASE"))
}

func TestMain(m *testing.M) {
	// Загружаем .env из корня проекта
	_ = godotenv.Load("../../../../.env")

	var err error
	testDB, err = gorm.Open(mysql.Open(mysqlDSN()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		fmt.Printf("Ошибка подключения к MySQL: %v\n", err)
		os.Exit(1)
	}

	// Cleanup от предыдущих запусков
	testDB.Exec("DELETE FROM outbox WHERE aggregate_id LIKE 'order-test-%'")
	testDB.Exec("DELETE FROM sagas WHERE order_id LIKE 'order-test-%'")
	testDB.Exec("DELETE FROM order_items WHERE order_id LIKE 'order-test-%'")
	testDB.Exec("DELETE FROM orders WHERE id LIKE 'order-test-%'")

	code := m.Run()

	// Cleanup после тестов
	testDB.Exec("DELETE FROM outbox WHERE aggregate_id LIKE 'order-test-%'")
	testDB.Exec("DELETE FROM sagas WHERE order_id LIKE 'order-test-%'")
	testDB.Exec("DELETE FROM order_items WHERE order_id LIKE 'order-test-%'")
	testDB.Exec("DELETE FROM orders WHERE id LIKE 'order-test-%'")

	os.Exit(code)
}

// generateTestID создаёт уникальный ID для теста.
func generateTestID(prefix string) string {
	return prefix + "-test-" + uuid.New().String()[:8]
}

// =============================================================================
// Тесты SagaRepository
// =============================================================================

func TestSagaRepository_CreateOrderWithSagaAndOutbox(t *testing.T) {
	repo := NewSagaRepository(testDB)
	ctx := context.Background()

	orderID := generateTestID("order")
	sagaID := generateTestID("saga")
	outboxID := generateTestID("outbox")

	order := &domain.Order{
		ID:     orderID,
		UserID: "user-123",
		Status: domain.OrderStatusPending,
		Items: []domain.OrderItem{
			{
				ID:          generateTestID("item"),
				OrderID:     orderID,
				ProductID:   "product-1",
				ProductName: "Тестовый товар",
				Quantity:    2,
				UnitPrice:   domain.Money{Amount: 5000, Currency: "RUB"},
			},
		},
		TotalAmount: domain.Money{Amount: 10000, Currency: "RUB"},
	}

	saga := &Saga{
		ID:       sagaID,
		OrderID:  orderID,
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	cmd := &Command{
		SagaID:   sagaID,
		OrderID:  orderID,
		Type:     CommandProcessPayment,
		Amount:   10000,
		Currency: "RUB",
	}
	outbox, err := NewOutbox(outboxID, orderID, "saga.commands", cmd, nil)
	require.NoError(t, err)

	// Act
	err = repo.CreateOrderWithSagaAndOutbox(ctx, order, saga, outbox)

	// Assert
	require.NoError(t, err)

	// Проверяем что order создан
	var orderCount int64
	testDB.Table("orders").Where("id = ?", orderID).Count(&orderCount)
	assert.Equal(t, int64(1), orderCount, "Order должен быть создан")

	// Проверяем что saga создана
	savedSaga, err := repo.GetByID(ctx, sagaID)
	require.NoError(t, err)
	assert.Equal(t, StatusPaymentPending, savedSaga.Status)
	assert.Equal(t, orderID, savedSaga.OrderID)

	// Проверяем что outbox создан
	var outboxCount int64
	testDB.Table("outbox").Where("id = ?", outboxID).Count(&outboxCount)
	assert.Equal(t, int64(1), outboxCount, "Outbox должен быть создан")
}

func TestSagaRepository_GetByID_NotFound(t *testing.T) {
	repo := NewSagaRepository(testDB)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "non-existent-saga")

	assert.ErrorIs(t, err, ErrSagaNotFound)
}

func TestSagaRepository_UpdateWithOrder(t *testing.T) {
	repo := NewSagaRepository(testDB)
	ctx := context.Background()

	// Arrange — создаём order + saga
	orderID := generateTestID("order")
	sagaID := generateTestID("saga")
	outboxID := generateTestID("outbox")

	order := &domain.Order{
		ID:          orderID,
		UserID:      "user-123",
		Status:      domain.OrderStatusPending,
		TotalAmount: domain.Money{Amount: 10000, Currency: "RUB"},
	}

	saga := &Saga{
		ID:       sagaID,
		OrderID:  orderID,
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	cmd := &Command{SagaID: sagaID, OrderID: orderID, Type: CommandProcessPayment}
	outbox, _ := NewOutbox(outboxID, orderID, "saga.commands", cmd, nil)

	err := repo.CreateOrderWithSagaAndOutbox(ctx, order, saga, outbox)
	require.NoError(t, err)

	// Act — обновляем сагу и заказ (успешная оплата)
	saga.Status = StatusCompleted
	paymentID := "payment-123"

	err = repo.UpdateWithOrder(ctx, saga, orderID, domain.OrderStatusConfirmed, &paymentID, nil)

	// Assert
	require.NoError(t, err)

	// Проверяем сагу
	updatedSaga, err := repo.GetByID(ctx, sagaID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, updatedSaga.Status)

	// Проверяем заказ
	var orderStatus string
	testDB.Table("orders").Where("id = ?", orderID).Pluck("status", &orderStatus)
	assert.Equal(t, "CONFIRMED", orderStatus)
}

func TestSagaRepository_UpdateWithOrderAndOutbox(t *testing.T) {
	repo := NewSagaRepository(testDB)
	ctx := context.Background()

	// Arrange
	orderID := generateTestID("order")
	sagaID := generateTestID("saga")
	outboxID := generateTestID("outbox")

	order := &domain.Order{
		ID:          orderID,
		UserID:      "user-123",
		Status:      domain.OrderStatusPending,
		TotalAmount: domain.Money{Amount: 10000, Currency: "RUB"},
	}

	saga := &Saga{
		ID:       sagaID,
		OrderID:  orderID,
		Status:   StatusPaymentPending,
		StepData: &StepData{Amount: 10000, Currency: "RUB"},
	}

	cmd := &Command{SagaID: sagaID, OrderID: orderID, Type: CommandProcessPayment}
	outbox, _ := NewOutbox(outboxID, orderID, "saga.commands", cmd, nil)

	err := repo.CreateOrderWithSagaAndOutbox(ctx, order, saga, outbox)
	require.NoError(t, err)

	// Act — компенсация с refund
	saga.Status = StatusFailed
	reason := "Платёж отклонён"

	refundOutboxID := generateTestID("outbox")
	refundCmd := &Command{SagaID: sagaID, OrderID: orderID, Type: CommandRefundPayment}
	refundOutbox, _ := NewOutbox(refundOutboxID, orderID, "saga.commands", refundCmd, nil)

	err = repo.UpdateWithOrderAndOutbox(ctx, saga, orderID, domain.OrderStatusFailed, nil, &reason, refundOutbox)

	// Assert
	require.NoError(t, err)

	// Проверяем сагу
	updatedSaga, err := repo.GetByID(ctx, sagaID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, updatedSaga.Status)

	// Проверяем refund outbox создан
	var refundCount int64
	testDB.Table("outbox").Where("id = ?", refundOutboxID).Count(&refundCount)
	assert.Equal(t, int64(1), refundCount, "Refund outbox должен быть создан")
}

// =============================================================================
// Тесты OutboxRepository
// =============================================================================

func TestOutboxRepository_GetUnprocessed(t *testing.T) {
	repo := NewOutboxRepository(testDB)
	ctx := context.Background()

	// Arrange — создаём 3 записи outbox напрямую
	for i := 0; i < 3; i++ {
		outboxID := generateTestID("outbox")
		orderID := generateTestID("order")
		testDB.Exec(`INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, topic, message_key, payload, created_at)
			VALUES (?, 'order', ?, 'PROCESS_PAYMENT', 'saga.commands', ?, '{}', NOW())`,
			outboxID, orderID, orderID)
	}

	// Act
	records, err := repo.GetUnprocessed(ctx, 10)

	// Assert
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(records), 3, "Должно быть минимум 3 записи")
}

func TestOutboxRepository_MarkProcessed(t *testing.T) {
	repo := NewOutboxRepository(testDB)
	ctx := context.Background()

	// Arrange
	outboxID := generateTestID("outbox")
	orderID := generateTestID("order")
	testDB.Exec(`INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, topic, message_key, payload, created_at)
		VALUES (?, 'order', ?, 'PROCESS_PAYMENT', 'saga.commands', ?, '{}', NOW())`,
		outboxID, orderID, orderID)

	// Act
	err := repo.MarkProcessed(ctx, outboxID)

	// Assert
	require.NoError(t, err)

	var processedAt *time.Time
	testDB.Table("outbox").Where("id = ?", outboxID).Pluck("processed_at", &processedAt)
	assert.NotNil(t, processedAt, "processed_at должен быть заполнен")
}

func TestOutboxRepository_MarkProcessed_NotFound(t *testing.T) {
	repo := NewOutboxRepository(testDB)
	ctx := context.Background()

	err := repo.MarkProcessed(ctx, "non-existent-outbox")

	assert.ErrorIs(t, err, ErrOutboxNotFound)
}

func TestOutboxRepository_MarkFailed(t *testing.T) {
	repo := NewOutboxRepository(testDB)
	ctx := context.Background()

	// Arrange
	outboxID := generateTestID("outbox")
	orderID := generateTestID("order")
	testDB.Exec(`INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, topic, message_key, payload, created_at, retry_count)
		VALUES (?, 'order', ?, 'PROCESS_PAYMENT', 'saga.commands', ?, '{}', NOW(), 0)`,
		outboxID, orderID, orderID)

	// Act
	err := repo.MarkFailed(ctx, outboxID, errors.New("kafka connection error"))

	// Assert
	require.NoError(t, err)

	var retryCount int
	var lastError string
	testDB.Table("outbox").Where("id = ?", outboxID).Pluck("retry_count", &retryCount)
	testDB.Table("outbox").Where("id = ?", outboxID).Pluck("last_error", &lastError)

	assert.Equal(t, 1, retryCount, "retry_count должен увеличиться")
	assert.Equal(t, "kafka connection error", lastError)
}
