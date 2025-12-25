package config

import "fmt"

// GRPCConfig содержит настройки gRPC портов всех сервисов.
type GRPCConfig struct {
	UserService    UserServiceConfig
	OrderService   OrderServiceConfig
	PaymentService PaymentServiceConfig
	APIGateway     APIGatewayConfig
}

// UserServiceConfig содержит настройки User Service.
type UserServiceConfig struct {
	Host string `env:"USER_SERVICE_HOST" envDefault:"localhost"`
	Port int    `env:"USER_SERVICE_PORT" envDefault:"50051"`
}

// Addr возвращает адрес User Service.
func (c UserServiceConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// OrderServiceConfig содержит настройки Order Service.
type OrderServiceConfig struct {
	Host string `env:"ORDER_SERVICE_HOST" envDefault:"localhost"`
	Port int    `env:"ORDER_SERVICE_PORT" envDefault:"50052"`
}

// Addr возвращает адрес Order Service.
func (c OrderServiceConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// PaymentServiceConfig содержит настройки Payment Service.
type PaymentServiceConfig struct {
	Host string `env:"PAYMENT_SERVICE_HOST" envDefault:"localhost"`
	Port int    `env:"PAYMENT_SERVICE_PORT" envDefault:"50053"`
}

// Addr возвращает адрес Payment Service.
func (c PaymentServiceConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// APIGatewayConfig содержит настройки API Gateway.
type APIGatewayConfig struct {
	Host string `env:"API_GATEWAY_HOST" envDefault:"0.0.0.0"`
	Port int    `env:"API_GATEWAY_PORT" envDefault:"8080"`
}

// Addr возвращает адрес API Gateway.
func (c APIGatewayConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
