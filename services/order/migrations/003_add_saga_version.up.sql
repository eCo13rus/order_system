-- Optimistic Locking: поле version для защиты от конкурентных обновлений саги.
-- При каждом UPDATE проверяем WHERE version = ? и инкрементируем version = version + 1.
-- Если RowsAffected == 0 — конкурентное обновление, транзакция откатывается.
ALTER TABLE sagas ADD COLUMN version INT UNSIGNED NOT NULL DEFAULT 1;
