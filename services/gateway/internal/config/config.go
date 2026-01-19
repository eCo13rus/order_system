// Package config содержит конфигурацию API Gateway.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// Config содержит полную конфигурацию API Gateway.
type Config struct {
	App       AppConfig
	HTTP      HTTPConfig
	Redis     RedisConfig
	JWT       JWTConfig
	GRPC      GRPCClientsConfig
	RateLimit RateLimitConfig
	Jaeger    JaegerConfig
	Metrics   MetricsConfig
}

// AppConfig — общие настройки приложения.
type AppConfig struct {
	Name      string `env:"APP_NAME" envDefault:"api-gateway"`
	Env       string `env:"APP_ENV" envDefault:"development"`
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogPretty bool   `env:"LOG_PRETTY" envDefault:"false"`
}

// HTTPConfig — настройки HTTP сервера.
type HTTPConfig struct {
	Host         string        `env:"HTTP_HOST" envDefault:"0.0.0.0"`
	Port         int           `env:"HTTP_PORT" envDefault:"8080"`
	ReadTimeout  time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"10s"`
	WriteTimeout time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"10s"`
	IdleTimeout  time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
}

// Addr возвращает адрес HTTP сервера.
func (c HTTPConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// RedisConfig — настройки подключения к Redis.
type RedisConfig struct {
	Host     string `env:"REDIS_HOST" envDefault:"localhost"`
	Port     int    `env:"REDIS_PORT" envDefault:"6379"`
	Password string `env:"REDIS_PASSWORD" envDefault:""`
	DB       int    `env:"REDIS_DB" envDefault:"0"`
}

// Addr возвращает адрес Redis сервера.
func (c RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// JWTConfig — настройки валидации JWT токенов.
// API Gateway использует только публичный ключ для верификации.
type JWTConfig struct {
	PublicKeyPath string `env:"JWT_PUBLIC_KEY_PATH,required"` // Путь к публичному ключу
	Issuer        string `env:"JWT_ISSUER" envDefault:"order-system"`
}

// GRPCClientsConfig — адреса gRPC сервисов.
type GRPCClientsConfig struct {
	UserServiceAddr    string `env:"GRPC_USER_SERVICE_ADDR" envDefault:"localhost:50051"`
	OrderServiceAddr   string `env:"GRPC_ORDER_SERVICE_ADDR" envDefault:"localhost:50052"`
	PaymentServiceAddr string `env:"GRPC_PAYMENT_SERVICE_ADDR" envDefault:"localhost:50053"`
}

// RateLimitConfig — настройки ограничения запросов.
type RateLimitConfig struct {
	Enabled       bool          `env:"RATE_LIMIT_ENABLED" envDefault:"true"`
	RequestsLimit int           `env:"RATE_LIMIT_REQUESTS" envDefault:"100"` // Количество запросов
	Window        time.Duration `env:"RATE_LIMIT_WINDOW" envDefault:"1m"`    // Временное окно
}

// JaegerConfig — настройки трассировки Jaeger.
type JaegerConfig struct {
	Enabled  bool   `env:"JAEGER_ENABLED" envDefault:"true"`
	Host     string `env:"JAEGER_HOST" envDefault:"localhost"`
	OTLPPort int    `env:"JAEGER_OTLP_PORT" envDefault:"4317"`
}

// OTLPEndpoint возвращает OTLP gRPC endpoint для Jaeger.
func (c JaegerConfig) OTLPEndpoint() string {
	return fmt.Sprintf("%s:%d", c.Host, c.OTLPPort)
}

// MetricsConfig — настройки Prometheus метрик.
type MetricsConfig struct {
	Enabled bool `env:"METRICS_ENABLED" envDefault:"true"` // Включить metrics endpoint
	Port    int  `env:"METRICS_PORT" envDefault:"9090"`    // Порт для /metrics
}

// Addr возвращает адрес для Metrics HTTP сервера.
func (c MetricsConfig) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// Load загружает конфигурацию из переменных окружения.
func Load() (*Config, error) {
	// Загружаем .env файл (игнорируем ошибку, если файл не найден)
	_ = godotenv.Load()

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга конфигурации: %w", err)
	}

	return cfg, nil
}

// IsDevelopment возвращает true в режиме разработки.
func (c *Config) IsDevelopment() bool {
	return c.App.Env == "development"
}
