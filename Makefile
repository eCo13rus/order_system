# =============================================================================
# Order Processing System - Makefile
# =============================================================================

.PHONY: help proto build up down logs test lint migrate clean jwt-keys jwt-keys-4096

# Переменные
COMPOSE_FILE := deployments/docker/docker-compose.yml
DOCKER_COMPOSE := docker compose -f $(COMPOSE_FILE)

# =============================================================================
# Help
# =============================================================================
help: ## Показать справку
	@echo "Order Processing System - Доступные команды:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# =============================================================================
# Proto
# =============================================================================
proto: ## Генерация Go кода из .proto файлов
	@echo "Генерация protobuf..."
	@protoc -I proto \
		--go_out=proto --go_opt=paths=source_relative \
		--go-grpc_out=proto --go-grpc_opt=paths=source_relative \
		proto/common/v1/common.proto \
		proto/user/v1/user.proto \
		proto/order/v1/order.proto \
		proto/payment/v1/payment.proto
	@echo "Готово!"

# =============================================================================
# Docker
# =============================================================================
build: ## Сборка Docker образов всех сервисов
	@echo "Сборка образов..."
	$(DOCKER_COMPOSE) build

up: ## Запуск инфраструктуры (MySQL, Redis, Kafka, Jaeger, Prometheus, Grafana)
	@echo "Запуск инфраструктуры..."
	$(DOCKER_COMPOSE) up -d
	@echo ""
	@echo "Сервисы запущены:"
	@echo "  MySQL:      localhost:3306 (локальный)"
	@echo "  Redis:      localhost:6379 (локальный)"
	@echo "  Kafka:      localhost:9092 (external: 9094)"
	@echo "  Kafka UI:   http://localhost:8090"
	@echo "  Jaeger:     http://localhost:16686"
	@echo "  Prometheus: http://localhost:9090"
	@echo "  Grafana:    http://localhost:3001 (admin/admin)"

down: ## Остановка инфраструктуры
	@echo "Остановка..."
	$(DOCKER_COMPOSE) down

down-v: ## Остановка + удаление volumes
	@echo "Остановка и удаление данных..."
	$(DOCKER_COMPOSE) down -v

logs: ## Показать логи всех контейнеров
	$(DOCKER_COMPOSE) logs -f

logs-%: ## Показать логи конкретного сервиса (make logs-kafka)
	$(DOCKER_COMPOSE) logs -f $*

ps: ## Статус контейнеров
	$(DOCKER_COMPOSE) ps

# =============================================================================
# Development
# =============================================================================
test: ## Запуск всех тестов
	@echo "Запуск тестов..."
	go test ./... -v -race -cover

test-unit: ## Только unit тесты
	go test ./... -v -short

test-integration: ## Integration тесты (требуют Docker)
	go test ./... -v -tags=integration

test-cover: ## Тесты с отчётом покрытия
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Отчёт: coverage.html"

lint: ## Линтер (golangci-lint)
	@echo "Проверка кода..."
	golangci-lint run ./...

fmt: ## Форматирование кода
	go fmt ./...
	goimports -w .

# =============================================================================
# Database
# =============================================================================
migrate: ## Применить миграции
	@echo "Применение миграций..."
	@for service in user order payment; do \
		echo "Миграции для $$service..."; \
		migrate -path services/$$service/migrations -database "mysql://root:root@tcp(localhost:3306)/order_system" up || true; \
	done

migrate-down: ## Откатить миграции
	@for service in user order payment; do \
		migrate -path services/$$service/migrations -database "mysql://root:root@tcp(localhost:3306)/order_system" down 1 || true; \
	done

migrate-create: ## Создать новую миграцию (make migrate-create name=create_users service=user)
	migrate create -ext sql -dir services/$(service)/migrations -seq $(name)

# =============================================================================
# Services
# =============================================================================
run-gateway: ## Запуск API Gateway
	go run ./services/gateway/cmd/main.go

run-user: ## Запуск User Service
	go run ./services/user/cmd/main.go

run-order: ## Запуск Order Service
	go run ./services/order/cmd/main.go

run-payment: ## Запуск Payment Service
	go run ./services/payment/cmd/main.go

# =============================================================================
# Cleanup
# =============================================================================
clean: ## Очистка артефактов сборки
	go clean
	rm -f coverage.out coverage.html
	rm -rf bin/

# =============================================================================
# JWT Keys (RS256)
# =============================================================================
jwt-keys: ## Генерация RSA ключей для JWT (RS256)
	@echo "Генерация RSA ключей для JWT..."
	@mkdir -p secrets/jwt
	@openssl genrsa -out secrets/jwt/private.pem 2048
	@openssl rsa -in secrets/jwt/private.pem -pubout -out secrets/jwt/public.pem
	@chmod 600 secrets/jwt/private.pem
	@chmod 644 secrets/jwt/public.pem
	@echo ""
	@echo "Ключи созданы:"
	@echo "  Приватный: secrets/jwt/private.pem (только User Service)"
	@echo "  Публичный: secrets/jwt/public.pem (все сервисы)"
	@echo ""
	@echo "ВАЖНО: Добавьте secrets/ в .gitignore!"

jwt-keys-4096: ## Генерация RSA 4096 ключей (повышенная безопасность)
	@echo "Генерация RSA 4096 ключей для JWT..."
	@mkdir -p secrets/jwt
	@openssl genrsa -out secrets/jwt/private.pem 4096
	@openssl rsa -in secrets/jwt/private.pem -pubout -out secrets/jwt/public.pem
	@chmod 600 secrets/jwt/private.pem
	@chmod 644 secrets/jwt/public.pem
	@echo "Ключи RSA 4096 созданы в secrets/jwt/"

# =============================================================================
# Dependencies
# =============================================================================
deps: ## Установка зависимостей
	go mod download
	go mod tidy

deps-tools: ## Установка инструментов разработки
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
