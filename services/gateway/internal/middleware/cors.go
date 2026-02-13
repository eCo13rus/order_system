// Package middleware — CORS middleware для обработки cross-origin запросов.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig — настройки CORS.
type CORSConfig struct {
	// AllowedOrigins — разрешённые источники. "*" разрешает все (только для dev).
	AllowedOrigins []string
	// AllowedMethods — разрешённые HTTP методы.
	AllowedMethods []string
	// AllowedHeaders — разрешённые заголовки запроса.
	AllowedHeaders []string
	// AllowCredentials — разрешить отправку cookies/auth headers.
	AllowCredentials bool
	// MaxAge — время кеширования preflight ответа (секунды).
	MaxAge string
}

// DefaultCORSConfig возвращает конфигурацию для development.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
		AllowCredentials: false, // false при AllowedOrigins=* (спецификация CORS)
		MaxAge:           "3600",
	}
}

// CORS создаёт middleware для обработки CORS preflight и основных запросов.
func CORS(cfg CORSConfig) gin.HandlerFunc {
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	origins := strings.Join(cfg.AllowedOrigins, ", ")

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}

		// Проверяем, разрешён ли origin
		allowed := false
		for _, o := range cfg.AllowedOrigins {
			if o == "*" || o == origin {
				allowed = true
				break
			}
		}

		if !allowed {
			c.Next()
			return
		}

		// Устанавливаем CORS заголовки
		h := c.Writer.Header()
		if origins == "*" {
			h.Set("Access-Control-Allow-Origin", "*")
		} else {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
		}
		h.Set("Access-Control-Allow-Methods", methods)
		h.Set("Access-Control-Allow-Headers", headers)
		h.Set("Access-Control-Max-Age", cfg.MaxAge)

		if cfg.AllowCredentials {
			h.Set("Access-Control-Allow-Credentials", "true")
		}

		// Preflight запрос — отвечаем сразу без обработки
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
