// Package config предоставляет загрузку конфигурации из переменных окружения.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// Config содержит полную конфигурацию приложения.
type Config struct {
	App     AppConfig
	MySQL   MySQLConfig
	Redis   RedisConfig
	Kafka   KafkaConfig
	JWT     JWTConfig
	Jaeger  JaegerConfig
	GRPC    GRPCConfig
	Metrics MetricsConfig
}

// AppConfig содержит общие настройки приложения.
type AppConfig struct {
	Name      string `env:"APP_NAME" envDefault:"order-system"`
	Env       string `env:"APP_ENV" envDefault:"development"`
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogPretty bool   `env:"LOG_PRETTY" envDefault:"false"`
}

// MySQLConfig содержит настройки подключения к MySQL.
type MySQLConfig struct {
	Host            string        `env:"MYSQL_HOST" envDefault:"localhost"`
	Port            int           `env:"MYSQL_PORT" envDefault:"3306"`
	User            string        `env:"MYSQL_USER" envDefault:"root"`
	Password        string        `env:"MYSQL_PASSWORD" envDefault:"root"`
	Database        string        `env:"MYSQL_DATABASE" envDefault:"order_system"`
	MaxOpenConns    int           `env:"MYSQL_MAX_OPEN_CONNS" envDefault:"25"`
	MaxIdleConns    int           `env:"MYSQL_MAX_IDLE_CONNS" envDefault:"10"`
	ConnMaxLifetime time.Duration `env:"MYSQL_CONN_MAX_LIFETIME" envDefault:"5m"`
}

// DSN возвращает строку подключения к MySQL.
func (c MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// RedisConfig содержит настройки подключения к Redis.
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

// KafkaConfig содержит настройки подключения к Kafka.
type KafkaConfig struct {
	Brokers       []string `env:"KAFKA_BROKERS" envDefault:"localhost:9092" envSeparator:","`
	ConsumerGroup string   `env:"KAFKA_CONSUMER_GROUP" envDefault:"order-system"`
}

// JWTConfig содержит настройки JWT токенов (RS256).
// PrivateKeyPath — только для сервиса, который выдаёт токены (User Service).
// PublicKeyPath — для всех сервисов, которые валидируют токены.
type JWTConfig struct {
	PrivateKeyPath  string        `env:"JWT_PRIVATE_KEY_PATH"`                    // Путь к приватному ключу (PEM)
	PublicKeyPath   string        `env:"JWT_PUBLIC_KEY_PATH,required"`            // Путь к публичному ключу (PEM)
	Issuer          string        `env:"JWT_ISSUER" envDefault:"order-system"`    // Издатель токена
	AccessTokenTTL  time.Duration `env:"JWT_ACCESS_TOKEN_TTL" envDefault:"15m"`   // Время жизни access token
	RefreshTokenTTL time.Duration `env:"JWT_REFRESH_TOKEN_TTL" envDefault:"168h"` // Время жизни refresh token (7 дней)
}

// JaegerConfig содержит настройки трассировки Jaeger.
type JaegerConfig struct {
	Enabled  bool   `env:"JAEGER_ENABLED" envDefault:"true"`
	Host     string `env:"JAEGER_HOST" envDefault:"localhost"`
	OTLPPort int    `env:"JAEGER_OTLP_PORT" envDefault:"4317"` // OTLP gRPC порт
}

// OTLPEndpoint возвращает OTLP gRPC endpoint для Jaeger.
func (c JaegerConfig) OTLPEndpoint() string {
	return fmt.Sprintf("%s:%d", c.Host, c.OTLPPort)
}

// MetricsConfig содержит настройки Prometheus метрик.
// В K8s все сервисы могут использовать один порт (разные pods).
// Локально — каждый сервис переопределяет METRICS_PORT.
type MetricsConfig struct {
	Enabled bool `env:"METRICS_ENABLED" envDefault:"true"` // Включить metrics endpoint
	Port    int  `env:"METRICS_PORT" envDefault:"9090"`    // Порт для /metrics
}

// Addr возвращает адрес для Metrics HTTP сервера.
func (c MetricsConfig) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// Load загружает конфигурацию из переменных окружения.
// Опционально загружает .env файл, если он существует.
func Load() (*Config, error) {
	// Пытаемся загрузить .env файл (игнорируем ошибку, если файл не найден)
	_ = godotenv.Load()

	cfg := &Config{}

	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга конфигурации: %w", err)
	}

	return cfg, nil
}

// LoadFromFile загружает конфигурацию из указанного .env файла.
func LoadFromFile(path string) (*Config, error) {
	if err := godotenv.Load(path); err != nil {
		return nil, fmt.Errorf("ошибка загрузки .env файла %s: %w", path, err)
	}

	cfg := &Config{}

	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга конфигурации: %w", err)
	}

	return cfg, nil
}

// IsDevelopment возвращает true, если приложение запущено в development режиме.
func (c *Config) IsDevelopment() bool {
	return c.App.Env == "development"
}

// IsProduction возвращает true, если приложение запущено в production режиме.
func (c *Config) IsProduction() bool {
	return c.App.Env == "production"
}
