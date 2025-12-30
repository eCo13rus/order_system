-- Откат миграции: удаление таблиц orders и order_items
-- Сервис: Order Service
-- Дата: 2025-12-30

-- Сначала удаляем дочернюю таблицу (из-за FK)
DROP TABLE IF EXISTS order_items;

-- Затем удаляем основную таблицу
DROP TABLE IF EXISTS orders;
