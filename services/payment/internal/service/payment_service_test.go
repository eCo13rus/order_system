package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/order-system/services/payment/internal/domain"
)

// =============================================================================
// Универсальный мок репозитория
// =============================================================================

// mockPaymentRepository — универсальный мок для всех тестов.
// Поддерживает настраиваемые ошибки и данные для recovery.
// Потокобезопасен для корректной эмуляции race condition тестов.
type mockPaymentRepository struct {
	mu       sync.Mutex // защита от race condition
	payments map[string]*domain.Payment
	bySaga   map[string]*domain.Payment

	// Настраиваемые ошибки (nil = нет ошибки)
	createErr error
	updateErr error
	getErr    error

	// Для теста RecoverStuckPayments
	stuckPayments []*domain.Payment
}

func newMockRepo() *mockPaymentRepository {
	return &mockPaymentRepository{
		payments: make(map[string]*domain.Payment),
		bySaga:   make(map[string]*domain.Payment),
	}
}

func (m *mockPaymentRepository) Create(ctx context.Context, payment *domain.Payment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createErr != nil {
		return m.createErr
	}
	// Проверяем идемпотентность (эмулирует UNIQUE constraint в БД)
	if _, exists := m.bySaga[payment.SagaID]; exists {
		return domain.ErrDuplicatePayment
	}

	payment.CreatedAt = time.Now()
	payment.UpdatedAt = time.Now()

	// Сохраняем копию — эмулируем INSERT в БД (снапшот данных, не ссылка)
	paymentCopy := *payment
	m.payments[payment.ID] = &paymentCopy
	m.bySaga[payment.SagaID] = &paymentCopy
	return nil
}

func (m *mockPaymentRepository) GetByID(ctx context.Context, id string) (*domain.Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return nil, m.getErr
	}
	if p, ok := m.payments[id]; ok {
		// Возвращаем копию, как реальная БД (каждый SELECT = новый объект)
		copy := *p
		return &copy, nil
	}
	return nil, domain.ErrPaymentNotFound
}

func (m *mockPaymentRepository) GetBySagaID(ctx context.Context, sagaID string) (*domain.Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return nil, m.getErr
	}
	if p, ok := m.bySaga[sagaID]; ok {
		// Возвращаем копию, как реальная БД (каждый SELECT = новый объект)
		copy := *p
		return &copy, nil
	}
	return nil, domain.ErrPaymentNotFound
}

func (m *mockPaymentRepository) Update(ctx context.Context, payment *domain.Payment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateErr != nil {
		return m.updateErr
	}
	if _, ok := m.payments[payment.ID]; !ok {
		return domain.ErrPaymentNotFound
	}
	payment.UpdatedAt = time.Now()
	m.payments[payment.ID] = payment
	m.bySaga[payment.SagaID] = payment
	return nil
}

func (m *mockPaymentRepository) GetStuckPending(ctx context.Context, olderThan time.Duration, limit int) ([]*domain.Payment, error) {
	return m.stuckPayments, nil
}

// =============================================================================
// Setup helper — убирает дублирование в тестах
// =============================================================================

// setupTest создаёт сервис с моками для тестирования.
func setupTest(t *testing.T) (*mockPaymentRepository, PaymentService) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := newMockRepo()
	svc := NewPaymentService(repo, rdb)
	return repo, svc
}

// =============================================================================
// Тесты ProcessPayment
// =============================================================================

func TestPaymentService_ProcessPayment_Success(t *testing.T) {
	repo, svc := setupTest(t)

	req := ProcessPaymentRequest{
		SagaID:   "saga-success-123",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   10000, // 100.00 RUB (не кратна 666)
		Currency: "RUB",
	}

	result, err := svc.ProcessPayment(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.PaymentID)
	assert.False(t, result.AlreadyExists)
	assert.Empty(t, result.FailureReason)

	// Проверяем, что платёж сохранён в репозитории
	saved, err := repo.GetBySagaID(context.Background(), req.SagaID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusCompleted, saved.Status)
}

func TestPaymentService_ProcessPayment_Failure(t *testing.T) {
	repo, svc := setupTest(t)

	req := ProcessPaymentRequest{
		SagaID:   "saga-fail-123",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   666, // Сумма кратна 666 — симуляция отказа
		Currency: "RUB",
	}

	result, err := svc.ProcessPayment(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.NotEmpty(t, result.PaymentID)
	assert.NotEmpty(t, result.FailureReason)

	// Проверяем, что платёж сохранён со статусом FAILED
	saved, err := repo.GetBySagaID(context.Background(), req.SagaID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusFailed, saved.Status)
}

func TestPaymentService_ProcessPayment_Idempotency(t *testing.T) {
	repo, svc := setupTest(t)

	req := ProcessPaymentRequest{
		SagaID:   "saga-idempotent-123",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   10000,
		Currency: "RUB",
	}

	// Первый запрос
	result1, err := svc.ProcessPayment(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.False(t, result1.AlreadyExists)

	// Повторный запрос с тем же saga_id
	result2, err := svc.ProcessPayment(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result2)

	assert.True(t, result2.AlreadyExists, "повторный запрос должен вернуть AlreadyExists=true")
	assert.Equal(t, result1.PaymentID, result2.PaymentID, "payment_id должен быть одинаковым")
	assert.Equal(t, result1.Success, result2.Success, "результат должен быть одинаковым")

	// Проверяем, что в БД только один платёж
	assert.Len(t, repo.payments, 1)
}

func TestPaymentService_ProcessPayment_InvalidAmount(t *testing.T) {
	_, svc := setupTest(t)

	req := ProcessPaymentRequest{
		SagaID:   "saga-invalid-123",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   0, // Невалидная сумма
		Currency: "RUB",
	}

	result, err := svc.ProcessPayment(context.Background(), req)

	require.NoError(t, err) // Не возвращаем ошибку, но результат — неуспешный
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.NotEmpty(t, result.FailureReason)
}

// =============================================================================
// Тесты RefundPayment
// =============================================================================

func TestPaymentService_RefundPayment_Success(t *testing.T) {
	repo, svc := setupTest(t)

	// Создаём платёж в статусе COMPLETED
	payment := &domain.Payment{
		ID:             "payment-refund-123",
		OrderID:        "order-123",
		SagaID:         "saga-123",
		UserID:         "user-123",
		Amount:         10000,
		Currency:       "RUB",
		Status:         domain.PaymentStatusCompleted,
		IdempotencyKey: "saga-123",
	}
	err := repo.Create(context.Background(), payment)
	require.NoError(t, err)

	err = svc.RefundPayment(context.Background(), RefundPaymentRequest{
		PaymentID: payment.ID,
		Reason:    "по запросу клиента",
	})

	require.NoError(t, err)

	// Проверяем статус
	updated, err := repo.GetByID(context.Background(), payment.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusRefunded, updated.Status)
	require.NotNil(t, updated.RefundID)
	require.NotNil(t, updated.RefundReason)
}

func TestPaymentService_RefundPayment_NotFound(t *testing.T) {
	_, svc := setupTest(t)

	err := svc.RefundPayment(context.Background(), RefundPaymentRequest{
		PaymentID: "non-existent",
		Reason:    "тест",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrPaymentNotFound)
}

func TestPaymentService_RefundPayment_InvalidStatus(t *testing.T) {
	repo, svc := setupTest(t)

	// Создаём платёж в статусе PENDING (нельзя возвращать)
	payment := &domain.Payment{
		ID:             "payment-pending-123",
		OrderID:        "order-123",
		SagaID:         "saga-123",
		UserID:         "user-123",
		Amount:         10000,
		Currency:       "RUB",
		Status:         domain.PaymentStatusPending,
		IdempotencyKey: "saga-123",
	}
	err := repo.Create(context.Background(), payment)
	require.NoError(t, err)

	err = svc.RefundPayment(context.Background(), RefundPaymentRequest{
		PaymentID: payment.ID,
		Reason:    "тест",
	})

	require.Error(t, err)

	// Статус не изменился
	updated, _ := repo.GetByID(context.Background(), payment.ID)
	assert.Equal(t, domain.PaymentStatusPending, updated.Status)
}

// =============================================================================
// Тесты GetPayment
// =============================================================================

func TestPaymentService_GetPayment_Success(t *testing.T) {
	repo, svc := setupTest(t)

	payment := &domain.Payment{
		ID:             "payment-get-123",
		OrderID:        "order-123",
		SagaID:         "saga-123",
		UserID:         "user-123",
		Amount:         10000,
		Currency:       "RUB",
		Status:         domain.PaymentStatusCompleted,
		IdempotencyKey: "saga-123",
	}
	err := repo.Create(context.Background(), payment)
	require.NoError(t, err)

	result, err := svc.GetPayment(context.Background(), payment.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, payment.ID, result.ID)
	assert.Equal(t, payment.Amount, result.Amount)
}

func TestPaymentService_GetPayment_NotFound(t *testing.T) {
	_, svc := setupTest(t)

	result, err := svc.GetPayment(context.Background(), "non-existent")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, domain.ErrPaymentNotFound)
}

// =============================================================================
// Тесты ошибок БД (используем тот же мок с настройкой ошибок)
// =============================================================================

func TestPaymentService_ProcessPayment_DBCreateError(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := newMockRepo()
	repo.createErr = errors.New("connection refused") // Настраиваем ошибку
	svc := NewPaymentService(repo, rdb)

	req := ProcessPaymentRequest{
		SagaID:   "saga-db-error",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   10000,
		Currency: "RUB",
	}

	result, err := svc.ProcessPayment(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPaymentService_ProcessPayment_DBUpdateError(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := newMockRepo()
	repo.updateErr = errors.New("deadlock detected") // Настраиваем ошибку
	svc := NewPaymentService(repo, rdb)

	req := ProcessPaymentRequest{
		SagaID:   "saga-update-error",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   10000,
		Currency: "RUB",
	}

	result, err := svc.ProcessPayment(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "deadlock detected")
}

// =============================================================================
// Тесты ошибок Redis
// =============================================================================

func TestPaymentService_ProcessPayment_RedisUnavailable(t *testing.T) {
	// Redis недоступен, но БД работает (fallback)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:59999"}) // Несуществующий порт
	t.Cleanup(func() { _ = rdb.Close() })

	repo := newMockRepo()
	svc := NewPaymentService(repo, rdb)

	req := ProcessPaymentRequest{
		SagaID:   "saga-redis-fail",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   10000,
		Currency: "RUB",
	}

	// Должен работать через fallback к БД
	result, err := svc.ProcessPayment(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.PaymentID)
}

// =============================================================================
// Тест race condition
// =============================================================================

func TestPaymentService_ProcessPayment_RaceCondition(t *testing.T) {
	repo, svc := setupTest(t)

	req := ProcessPaymentRequest{
		SagaID:   "saga-race-123",
		OrderID:  "order-123",
		UserID:   "user-123",
		Amount:   10000,
		Currency: "RUB",
	}

	// Запускаем два запроса параллельно
	results := make(chan *ProcessPaymentResult, 2)
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			result, err := svc.ProcessPayment(context.Background(), req)
			results <- result
			errs <- err
		}()
	}

	// Собираем результаты
	var allResults []*ProcessPaymentResult
	for i := 0; i < 2; i++ {
		err := <-errs
		result := <-results
		require.NoError(t, err)
		require.NotNil(t, result)
		allResults = append(allResults, result)
	}

	// Оба запроса успешны, payment_id одинаковый
	assert.Equal(t, allResults[0].PaymentID, allResults[1].PaymentID, "payment_id должен быть одинаковым")

	// Минимум один из них должен быть AlreadyExists=true
	alreadyExistsCount := 0
	for _, r := range allResults {
		if r.AlreadyExists {
			alreadyExistsCount++
		}
	}
	assert.GreaterOrEqual(t, alreadyExistsCount, 1, "минимум один запрос должен вернуть AlreadyExists=true")

	// В репозитории только один платёж
	assert.Len(t, repo.payments, 1)
}

// =============================================================================
// Тест recovery зависших платежей
// =============================================================================

func TestPaymentService_RecoverStuckPayments(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// Создаём зависшие платежи
	stuckPayment1 := &domain.Payment{
		ID:      "stuck-1",
		SagaID:  "saga-stuck-1",
		OrderID: "order-1",
		UserID:  "user-1",
		Amount:  1000,
		Status:  domain.PaymentStatusPending,
	}
	stuckPayment2 := &domain.Payment{
		ID:      "stuck-2",
		SagaID:  "saga-stuck-2",
		OrderID: "order-2",
		UserID:  "user-2",
		Amount:  2000,
		Status:  domain.PaymentStatusPending,
	}

	repo := newMockRepo()
	repo.stuckPayments = []*domain.Payment{stuckPayment1, stuckPayment2}
	repo.payments["stuck-1"] = stuckPayment1
	repo.payments["stuck-2"] = stuckPayment2

	svc := NewPaymentService(repo, rdb)

	recovered, err := svc.RecoverStuckPayments(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 2, recovered)

	// Проверяем что статусы изменились на FAILED
	assert.Equal(t, domain.PaymentStatusFailed, repo.payments["stuck-1"].Status)
	assert.Equal(t, domain.PaymentStatusFailed, repo.payments["stuck-2"].Status)
	assert.NotNil(t, repo.payments["stuck-1"].FailureReason)
}

func TestPaymentService_RecoverStuckPayments_NoStuck(t *testing.T) {
	repo, svc := setupTest(t)
	repo.stuckPayments = nil

	recovered, err := svc.RecoverStuckPayments(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 0, recovered)
}
