# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Проект

Order Processing System — микросервисная система обработки заказов на Go 1.22+ с Saga Orchestration.

## Технологический стек

- **Язык:** Go 1.22+
- **API:** gRPC (межсервисное) + REST/Gin (внешний gateway)
- **БД:** MySQL 8.0 + GORM
- **Очереди:** Apache Kafka 3.x (KRaft, без Zookeeper)
- **Кэш:** Redis (rate limiting, JWT blacklist, кэш)
- **Observability:** Prometheus + Grafana + Jaeger
- **Логирование:** Zerolog (логи на русском языке)
- **Контейнеризация:** Docker + Kubernetes

## Архитектура

```
Клиенты → API Gateway (Gin :8080) → gRPC сервисы
                                    ├── User Service (:50051)
                                    ├── Order Service (:50052) — Saga Orchestrator
                                    └── Payment Service (:50053)

Асинхронное взаимодействие: Kafka (saga.commands, saga.replies, dlq.saga)
```

**Ключевые паттерны:**
- Clean Architecture (domain/repository/service/handler)
- Saga Orchestration — распределённые транзакции с компенсациями
- Outbox Pattern — гарантия доставки в Kafka (атомарная запись заказ + outbox)
- Circuit Breaker (порог 5 ошибок → OPEN, таймаут 30 сек)
- JWT Blacklist в Redis (инвалидация по jti)

## Команды

```bash
make proto          # Генерация Go кода из .proto файлов
make build          # Сборка Docker образов
make up             # Запуск всей системы (docker-compose)
make down           # Остановка
make test           # go test ./... -v -cover
make lint           # golangci-lint run
make migrate        # Применение SQL миграций
```

## Структура проекта

```
proto/              # gRPC контракты (*.proto)
pkg/                # Общие пакеты
  ├── logger/       # Zerolog, логи на RU
  ├── config/       # ENV/YAML конфигурация
  ├── kafka/        # Producer/Consumer обёртки
  └── middleware/   # gRPC interceptors (tracing, logging)
services/           # Микросервисы
  └── <service>/
      ├── cmd/main.go
      ├── internal/
      │   ├── domain/      # Бизнес-сущности
      │   ├── repository/  # Работа с БД
      │   ├── service/     # Бизнес-логика
      │   ├── grpc/        # gRPC handlers
      │   └── saga/        # Saga координатор (Order Service)
      └── migrations/      # SQL миграции
deployments/        # Docker + Kubernetes манифесты
.github/workflows/  # CI/CD пайплайны
```

## Saga Flow (создание заказа)

1. Order Service: создать заказ (PENDING) → записать в outbox
2. Outbox Worker → Kafka `saga.commands`
3. Payment Service: списать средства → Kafka `saga.replies`
4. Order Service: подтвердить (CONFIRMED) или откатить (компенсация)

**Состояния Saga:** STARTED → PAYMENT_PENDING → COMPLETED | COMPENSATING → FAILED

## Redis ключи

| Ключ | TTL | Назначение |
|------|-----|------------|
| `jwt:blacklist:{jti}` | remaining TTL токена | Отозванные JWT |
| `rate:{ip}` | 1 мин | Rate limiting |
| `order:{id}` | 10 мин | Кэш заказа |
| `idempotency:{key}` | 24 ч | Идемпотентность |

## Тестирование

```bash
go test ./... -v -race -cover           # Все тесты
go test ./services/order/... -v         # Тесты конкретного сервиса
go test -run TestSagaCoordinator -v     # Один тест
```

### Глобальные агенты (использовать при необходимости)

- `golang-pro` — сложная Go логика, рефакторинг
- `debugger` — отладка ошибок
- `security-auditor` — проверка безопасности
- `code-reviewer` — code review
