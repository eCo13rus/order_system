-- Откат миграции: удаление таблиц Saga и Outbox
-- ВНИМАНИЕ: Удаляет все данные саг и outbox!

DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS sagas;
