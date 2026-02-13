# Универсальный Dockerfile для всех сервисов
# Использование: docker build --build-arg SERVICE=gateway -t order-system-gateway .

FROM golang:1.24-alpine AS builder

ARG SERVICE
RUN test -n "$SERVICE" || (echo "SERVICE arg required" && exit 1)

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/service ./services/${SERVICE}/cmd/main.go

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/service .
USER nobody
ENTRYPOINT ["./service"]
