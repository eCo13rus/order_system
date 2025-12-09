.PHONY: proto build up down test lint

# Генерация Go кода из proto файлов
proto:
	protoc -I proto/user \
		--go_out=proto/user --go_opt=paths=source_relative \
		--go-grpc_out=proto/user --go-grpc_opt=paths=source_relative \
		proto/user/user.proto
	protoc -I proto/order \
		--go_out=proto/order --go_opt=paths=source_relative \
		--go-grpc_out=proto/order --go-grpc_opt=paths=source_relative \
		proto/order/order.proto
	protoc -I proto/payment \
		--go_out=proto/payment --go_opt=paths=source_relative \
		--go-grpc_out=proto/payment --go-grpc_opt=paths=source_relative \
		proto/payment/payment.proto

# Сборка всех сервисов
build:
	go build -o bin/gateway ./services/gateway/cmd
	go build -o bin/user ./services/user/cmd
	go build -o bin/order ./services/order/cmd
	go build -o bin/payment ./services/payment/cmd

# Запуск инфраструктуры
up:
	docker-compose up -d

# Остановка инфраструктуры
down:
	docker-compose down

# Запуск тестов
test:
	go test ./... -v -race -cover

# Линтер
lint:
	golangci-lint run ./...
