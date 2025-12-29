-- Миграция: создание таблицы пользователей
-- Сервис: User Service
-- Дата: 2025-12-26

-- Таблица users хранит данные пользователей системы
CREATE TABLE IF NOT EXISTS users (
    -- UUID в качестве первичного ключа для распределённой генерации
    id VARCHAR(36) PRIMARY KEY,

    -- Имя пользователя для отображения
    name VARCHAR(100) NOT NULL,

    -- Email пользователя, уникальный, используется для аутентификации
    email VARCHAR(255) NOT NULL,

    -- Хеш пароля (bcrypt)
    password VARCHAR(255) NOT NULL,

    -- Временные метки
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- Уникальный индекс на email для быстрого поиска и проверки уникальности
    UNIQUE INDEX idx_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
