-- Миграция: создание таблиц для Saga Orchestration и Outbox Pattern
-- Сервис: Order Service
-- Дата: 2025-12-30

-- ============================================================================
-- Таблица sagas: хранит состояние Saga-транзакций
-- ============================================================================
-- Saga Orchestrator отслеживает прогресс распределённой транзакции.
-- Каждый заказ имеет связанную Saga, которая координирует шаги:
-- 1. Создание заказа (PENDING)
-- 2. Запрос оплаты в Payment Service
-- 3. Подтверждение или откат заказа
CREATE TABLE IF NOT EXISTS sagas (
    -- UUID саги (генерируется при создании заказа)
    id VARCHAR(36) PRIMARY KEY,

    -- UUID заказа (один заказ = одна сага)
    order_id VARCHAR(36) NOT NULL,

    -- Состояние саги:
    -- PAYMENT_PENDING - начальное состояние, команда ProcessPayment в outbox
    -- COMPLETED       - платёж успешен, заказ подтверждён (CONFIRMED)
    -- COMPENSATING    - получена ошибка, выполняем откат
    -- FAILED          - сага завершена с ошибкой, заказ помечен FAILED
    status VARCHAR(20) NOT NULL DEFAULT 'PAYMENT_PENDING',

    -- JSON с данными для текущего шага саги
    -- Пример: {"payment_id": "uuid", "amount": 10000, "currency": "RUB"}
    step_data JSON NULL,

    -- Причина ошибки (заполняется при FAILED)
    failure_reason TEXT NULL,

    -- Временные метки
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- Один заказ = одна сага
    UNIQUE INDEX idx_sagas_order_id (order_id),

    -- Индекс для поиска активных саг (для мониторинга и recovery)
    INDEX idx_sagas_status (status),

    -- Внешний ключ на заказ
    CONSTRAINT fk_sagas_order_id
        FOREIGN KEY (order_id) REFERENCES orders(id)
        ON DELETE CASCADE
        ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ============================================================================
-- Таблица outbox: Outbox Pattern для гарантированной доставки в Kafka
-- ============================================================================
-- Outbox Pattern решает проблему "dual write":
-- Нельзя атомарно записать в БД И отправить в Kafka.
-- Решение: пишем всё в БД в одной транзакции, отдельный worker читает и отправляет.
--
-- Поток данных:
-- 1. Бизнес-логика в транзакции: UPDATE orders + INSERT INTO outbox
-- 2. Outbox Worker: SELECT FROM outbox WHERE processed_at IS NULL
-- 3. Отправка в Kafka
-- 4. UPDATE outbox SET processed_at = NOW()
CREATE TABLE IF NOT EXISTS outbox (
    -- UUID записи
    id VARCHAR(36) PRIMARY KEY,

    -- Тип агрегата (order, payment) — для фильтрации и роутинга
    aggregate_type VARCHAR(50) NOT NULL,

    -- ID агрегата (order_id) — для партиционирования в Kafka (один заказ = одна партиция)
    aggregate_id VARCHAR(36) NOT NULL,

    -- Тип события/команды:
    -- saga.command.process_payment — команда на обработку платежа
    -- saga.command.refund_payment  — команда на возврат платежа
    event_type VARCHAR(100) NOT NULL,

    -- Топик Kafka куда отправлять сообщение
    topic VARCHAR(100) NOT NULL,

    -- Ключ сообщения Kafka (обычно aggregate_id для партиционирования)
    message_key VARCHAR(100) NOT NULL,

    -- JSON payload сообщения
    -- Пример: {"saga_id": "uuid", "order_id": "uuid", "amount": 10000, "currency": "RUB"}
    payload JSON NOT NULL,

    -- JSON с headers для Kafka (trace_id, correlation_id)
    headers JSON NULL,

    -- Когда запись создана
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Когда запись обработана (NULL = ещё не обработана)
    -- После успешной отправки в Kafka проставляется timestamp
    processed_at TIMESTAMP NULL,

    -- Количество попыток отправки (для retry с backoff)
    retry_count INT UNSIGNED NOT NULL DEFAULT 0,

    -- Последняя ошибка при отправке (для диагностики)
    last_error TEXT NULL,

    -- Индекс для Outbox Worker: выбираем необработанные записи по времени создания
    -- Worker делает: SELECT * FROM outbox WHERE processed_at IS NULL ORDER BY created_at LIMIT 100
    INDEX idx_outbox_unprocessed (processed_at, created_at),

    -- Индекс для поиска по агрегату (диагностика, просмотр истории)
    INDEX idx_outbox_aggregate (aggregate_type, aggregate_id),

    -- Индекс для мониторинга застрявших записей (retry_count > 0)
    INDEX idx_outbox_retry (retry_count, processed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
