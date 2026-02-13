package db

import (
	"github.com/redis/go-redis/v9"

	"example.com/order-system/pkg/config"
)

// ConnectRedis создаёт клиент Redis.
func ConnectRedis(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr(),
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}
