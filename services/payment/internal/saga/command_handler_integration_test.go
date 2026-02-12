//go:build integration

// Package saga — интеграционные тесты для Kafka Consumer/Producer.
// Требует: Kafka, MySQL, Redis (настройки из .env).
// Запуск: go test -tags=integration -v ./services/payment/internal/saga/...
package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	pkgkafka "example.com/order-system/pkg/kafka"
	"example.com/order-system/services/payment/internal/outbox"
	"example.com/order-system/services/payment/internal/repository"
	"example.com/order-system/services/payment/internal/service"
)

// =============================================================================
// Конфигурация тестов
// =============================================================================

const (
	consumerGroup = "payment-integration-test"
	testTimeout   = 10 * time.Second
)

// kafkaBroker возвращает адрес Kafka из .env
func kafkaBroker() string { return os.Getenv("KAFKA_BROKERS") }

// mysqlDSN собирает DSN из переменных .env
func mysqlDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		os.Getenv("MYSQL_USER"), os.Getenv("MYSQL_PASSWORD"),
		os.Getenv("MYSQL_HOST"), os.Getenv("MYSQL_PORT"), os.Getenv("MYSQL_DATABASE"))
}

// =============================================================================
// Глобальные переменные для TestMain
// =============================================================================

var (
	testDB        *gorm.DB
	testRedis     *redis.Client
	miniRedis     *miniredis.Miniredis
	testProducer  *kafka.Writer // Для отправки команд в saga.commands
	testConsumer  *kafka.Reader // Для чтения ответов из saga.replies
	handler       *CommandHandler
	handlerCtx    context.Context
	handlerCancel context.CancelFunc
)

// =============================================================================
// TestMain — инициализация инфраструктуры
// =============================================================================

func TestMain(m *testing.M) {
	// Загружаем .env из корня проекта
	_ = godotenv.Load("../../../../.env")

	var err error

	// 1. MySQL — БД order_system
	testDB, err = gorm.Open(mysql.Open(mysqlDSN()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		fmt.Printf("Ошибка подключения к MySQL: %v\n", err)
		os.Exit(1)
	}

	// Очищаем тестовые данные от предыдущих запусков
	testDB.Exec("DELETE FROM payments WHERE saga_id LIKE 'saga-test-%'")
	testDB.Exec("DELETE FROM outbox WHERE aggregate_id LIKE 'order-%'")

	// 2. Redis — miniredis (встроенный мок)
	miniRedis, err = miniredis.Run()
	if err != nil {
		fmt.Printf("Ошибка запуска miniredis: %v\n", err)
		os.Exit(1)
	}
	testRedis = redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})

	// 3. Kafka Producer — для отправки тестовых команд
	testProducer = &kafka.Writer{
		Addr:     kafka.TCP(kafkaBroker()),
		Topic:    pkgkafka.TopicSagaCommands,
		Balancer: &kafka.LeastBytes{},
	}

	// 4. Kafka Consumer — для чтения ответов
	testConsumer = kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{kafkaBroker()},
		Topic:    pkgkafka.TopicSagaReplies,
		GroupID:  consumerGroup + "-" + uuid.New().String()[:8], // Уникальная группа
		MinBytes: 1,
		MaxBytes: 10e6,
		MaxWait:  100 * time.Millisecond,
	})

	// 5. Запускаем CommandHandler + OutboxWorker в фоне
	if err := startHandler(); err != nil {
		fmt.Printf("Ошибка запуска handler: %v\n", err)
		os.Exit(1)
	}

	// Даём время на инициализацию consumer group
	time.Sleep(500 * time.Millisecond)

	// Запускаем тесты
	code := m.Run()

	// Cleanup
	handlerCancel()
	testProducer.Close()
	testConsumer.Close()
	miniRedis.Close()

	os.Exit(code)
}

// startHandler запускает CommandHandler + OutboxWorker для обработки команд.
func startHandler() error {
	// Создаём зависимости
	repo := repository.NewPaymentRepository(testDB)
	paymentSvc := service.NewPaymentService(repo, testRedis)

	// Outbox Repository для записи reply
	outboxRepo := outbox.NewOutboxRepository(testDB)

	// Kafka consumer для handler
	cfg := pkgkafka.Config{Brokers: []string{kafkaBroker()}}
	consumer, err := pkgkafka.NewConsumer(cfg, pkgkafka.TopicSagaCommands, consumerGroup)
	if err != nil {
		return err
	}

	// Kafka producer для Outbox Worker
	producer, err := pkgkafka.NewProducer(cfg)
	if err != nil {
		return err
	}

	// Command Handler использует outbox
	handler = NewCommandHandler(consumer, outboxRepo, paymentSvc)

	// Outbox Worker отправляет reply из outbox в Kafka
	outboxWorker := outbox.NewOutboxWorker(outboxRepo, producer, outbox.DefaultWorkerConfig())

	// Запускаем в горутинах
	handlerCtx, handlerCancel = context.WithCancel(context.Background())
	go func() {
		_ = handler.Run(handlerCtx)
	}()
	go func() {
		outboxWorker.Run(handlerCtx)
	}()

	return nil
}

// =============================================================================
// Хелперы — AAA паттерн
// =============================================================================

// sendCommand отправляет команду в saga.commands.
func sendCommand(t *testing.T, cmd Command) {
	t.Helper()

	data, err := json.Marshal(cmd)
	require.NoError(t, err, "Ошибка сериализации команды")

	err = testProducer.WriteMessages(context.Background(), kafka.Message{
		Key:   []byte(cmd.SagaID),
		Value: data,
	})
	require.NoError(t, err, "Ошибка отправки команды в Kafka")
}

// receiveReply читает ответ из saga.replies с таймаутом.
func receiveReply(t *testing.T, sagaID string, timeout time.Duration) *Reply {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		msg, err := testConsumer.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatalf("Таймаут ожидания ответа для saga_id=%s", sagaID)
			}
			t.Fatalf("Ошибка чтения из Kafka: %v", err)
		}

		var reply Reply
		if err := json.Unmarshal(msg.Value, &reply); err != nil {
			continue // Пропускаем битые сообщения
		}

		// Проверяем что это ответ на нашу сагу
		if reply.SagaID == sagaID {
			return &reply
		}
	}
}

// generateSagaID создаёт уникальный saga_id для теста.
func generateSagaID() string {
	return "saga-test-" + uuid.New().String()[:8]
}

// cleanupPayment удаляет платёж и outbox записи после теста.
func cleanupPayment(sagaID string) {
	testDB.Exec("DELETE FROM payments WHERE saga_id = ?", sagaID)
	testDB.Exec("DELETE FROM outbox WHERE message_key = ?", sagaID)
}

// =============================================================================
// Тесты — 4 кейса
// =============================================================================

// TestProcessPayment_Success проверяет успешную обработку платежа.
func TestProcessPayment_Success(t *testing.T) {
	// Arrange
	sagaID := generateSagaID()
	defer cleanupPayment(sagaID)

	cmd := Command{
		SagaID:    sagaID,
		OrderID:   "order-success-123",
		Type:      CommandProcessPayment,
		Amount:    10000, // 100.00 — не кратно 666
		Currency:  "RUB",
		UserID:    "user-123",
		Timestamp: time.Now(),
	}

	// Act
	sendCommand(t, cmd)
	reply := receiveReply(t, sagaID, testTimeout)

	// Assert
	assert.Equal(t, sagaID, reply.SagaID, "SagaID должен совпадать")
	assert.Equal(t, cmd.OrderID, reply.OrderID, "OrderID должен совпадать")
	assert.Equal(t, ReplySuccess, reply.Status, "Статус должен быть SUCCESS")
	assert.NotEmpty(t, reply.PaymentID, "PaymentID должен быть заполнен")
	assert.Empty(t, reply.Error, "Error должен быть пустым")
}

// TestProcessPayment_FailedAmount666 проверяет отклонение платежа (сумма кратна 666).
func TestProcessPayment_FailedAmount666(t *testing.T) {
	// Arrange
	sagaID := generateSagaID()
	defer cleanupPayment(sagaID)

	cmd := Command{
		SagaID:    sagaID,
		OrderID:   "order-fail-666",
		Type:      CommandProcessPayment,
		Amount:    6660, // Кратно 666 → симуляция отказа
		Currency:  "RUB",
		UserID:    "user-123",
		Timestamp: time.Now(),
	}

	// Act
	sendCommand(t, cmd)
	reply := receiveReply(t, sagaID, testTimeout)

	// Assert
	assert.Equal(t, sagaID, reply.SagaID, "SagaID должен совпадать")
	assert.Equal(t, ReplyFailed, reply.Status, "Статус должен быть FAILED")
	assert.NotEmpty(t, reply.Error, "Error должен содержать причину")
}

// TestProcessPayment_Idempotency проверяет идемпотентность (повторный запрос).
func TestProcessPayment_Idempotency(t *testing.T) {
	// Arrange — первый запрос
	sagaID := generateSagaID()
	defer cleanupPayment(sagaID)

	cmd := Command{
		SagaID:    sagaID,
		OrderID:   "order-idem-123",
		Type:      CommandProcessPayment,
		Amount:    5000,
		Currency:  "RUB",
		UserID:    "user-123",
		Timestamp: time.Now(),
	}

	sendCommand(t, cmd)
	firstReply := receiveReply(t, sagaID, testTimeout)
	require.Equal(t, ReplySuccess, firstReply.Status, "Первый запрос должен быть успешным")

	// Act — повторный запрос с тем же saga_id
	sendCommand(t, cmd)
	secondReply := receiveReply(t, sagaID, testTimeout)

	// Assert — тот же результат, тот же payment_id
	assert.Equal(t, ReplySuccess, secondReply.Status, "Повторный запрос тоже SUCCESS")
	assert.Equal(t, firstReply.PaymentID, secondReply.PaymentID, "PaymentID должен совпадать (идемпотентность)")
}

// TestRefundPayment_Success проверяет успешный возврат платежа.
func TestRefundPayment_Success(t *testing.T) {
	// Arrange — сначала создаём платёж
	sagaID := generateSagaID()
	defer cleanupPayment(sagaID)

	processCmd := Command{
		SagaID:    sagaID,
		OrderID:   "order-refund-123",
		Type:      CommandProcessPayment,
		Amount:    10000,
		Currency:  "RUB",
		UserID:    "user-123",
		Timestamp: time.Now(),
	}

	sendCommand(t, processCmd)
	processReply := receiveReply(t, sagaID, testTimeout)
	require.Equal(t, ReplySuccess, processReply.Status, "Платёж должен быть создан")

	// Act — возврат
	refundCmd := Command{
		SagaID:    sagaID,
		OrderID:   "order-refund-123",
		Type:      CommandRefundPayment,
		Timestamp: time.Now(),
	}

	sendCommand(t, refundCmd)
	refundReply := receiveReply(t, sagaID, testTimeout)

	// Assert
	assert.Equal(t, sagaID, refundReply.SagaID, "SagaID должен совпадать")
	assert.Equal(t, ReplySuccess, refundReply.Status, "Возврат должен быть успешным")
}
