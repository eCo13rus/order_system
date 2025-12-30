-- Миграция: создание таблиц orders и order_items
-- Сервис: Order Service
-- Дата: 2025-12-30

-- Таблица orders хранит заказы пользователей
CREATE TABLE IF NOT EXISTS orders (
    -- UUID в качестве первичного ключа для распределённой генерации
    id VARCHAR(36) PRIMARY KEY,

    -- UUID пользователя (связь с User Service по UUID, без FK между микросервисами)
    user_id VARCHAR(36) NOT NULL,

    -- Статус заказа: PENDING, CONFIRMED, CANCELLED, FAILED
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',

    -- Общая сумма заказа в минимальных единицах валюты (копейки/центы)
    total_amount BIGINT NOT NULL DEFAULT 0,

    -- Код валюты по ISO 4217 (RUB, USD, EUR и т.д.)
    currency VARCHAR(3) NOT NULL DEFAULT 'RUB',

    -- Ключ идемпотентности для защиты от дублирования заказов
    idempotency_key VARCHAR(64) NULL,

    -- UUID платежа (связь с Payment Service, заполняется после создания платежа)
    payment_id VARCHAR(36) NULL,

    -- Причина отказа/отмены заказа (заполняется при status = FAILED или CANCELLED)
    failure_reason TEXT NULL,

    -- Временные метки
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- Индексы для частых запросов
    INDEX idx_orders_user_id (user_id),
    INDEX idx_orders_status (status),
    INDEX idx_orders_created_at (created_at),
    -- Составной индекс для фильтрации заказов пользователя по статусу
    INDEX idx_orders_user_status (user_id, status),
    -- Уникальный индекс для идемпотентности (один ключ = один заказ)
    UNIQUE INDEX idx_orders_idempotency_key (idempotency_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- Таблица order_items хранит позиции заказа
CREATE TABLE IF NOT EXISTS order_items (
    -- UUID в качестве первичного ключа
    id VARCHAR(36) PRIMARY KEY,

    -- UUID заказа (внешний ключ на orders.id)
    order_id VARCHAR(36) NOT NULL,

    -- UUID продукта (для будущей интеграции с Product Service)
    product_id VARCHAR(36) NOT NULL,

    -- Название продукта (денормализовано для сохранения истории)
    product_name VARCHAR(255) NOT NULL,

    -- Количество единиц товара
    quantity INT UNSIGNED NOT NULL DEFAULT 1,

    -- Цена за единицу в минимальных единицах валюты (копейки/центы)
    unit_price BIGINT NOT NULL,

    -- Код валюты по ISO 4217 (должен совпадать с валютой заказа)
    currency VARCHAR(3) NOT NULL DEFAULT 'RUB',

    -- Временные метки
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- Внешний ключ с каскадным удалением (удаление заказа удаляет позиции)
    CONSTRAINT fk_order_items_order_id
        FOREIGN KEY (order_id) REFERENCES orders(id)
        ON DELETE CASCADE
        ON UPDATE CASCADE,

    -- Индекс для быстрого получения позиций заказа
    INDEX idx_order_items_order_id (order_id),
    -- Индекс для поиска заказов по продукту (аналитика)
    INDEX idx_order_items_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
