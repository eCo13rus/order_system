-- Миграция: создание таблицы payments
-- Сервис: Payment Service
-- Дата: 2026-01-10

-- Таблица payments хранит платежи для Saga Orchestration
CREATE TABLE IF NOT EXISTS payments (
    -- UUID платежа (первичный ключ)
    id VARCHAR(36) PRIMARY KEY,

    -- UUID заказа (связь с Order Service, без FK между микросервисами)
    order_id VARCHAR(36) NOT NULL,

    -- UUID саги для корреляции с Order Service
    saga_id VARCHAR(36) NOT NULL,

    -- UUID пользователя
    user_id VARCHAR(36) NOT NULL,

    -- Сумма платежа в минимальных единицах (копейки/центы)
    amount BIGINT NOT NULL,

    -- Код валюты по ISO 4217 (RUB, USD, EUR)
    currency VARCHAR(3) NOT NULL DEFAULT 'RUB',

    -- Статус платежа: PENDING, COMPLETED, FAILED, REFUNDED
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',

    -- Метод оплаты (card, wallet и т.д.)
    payment_method VARCHAR(50) NOT NULL DEFAULT 'card',

    -- Причина ошибки (заполняется при status = FAILED)
    failure_reason TEXT NULL,

    -- UUID возврата (заполняется при status = REFUNDED)
    refund_id VARCHAR(36) NULL,

    -- Причина возврата
    refund_reason TEXT NULL,

    -- Ключ идемпотентности (saga_id используется как ключ)
    idempotency_key VARCHAR(64) NOT NULL,

    -- Временные метки
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- Индексы
    INDEX idx_payments_order_id (order_id),
    INDEX idx_payments_saga_id (saga_id),
    INDEX idx_payments_user_id (user_id),
    INDEX idx_payments_status (status),
    INDEX idx_payments_created_at (created_at),

    -- Уникальный индекс для идемпотентности (одна сага = один платёж)
    UNIQUE INDEX idx_payments_idempotency_key (idempotency_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
