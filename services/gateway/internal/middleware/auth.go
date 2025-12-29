// Package middleware содержит HTTP middleware для API Gateway.
package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/gateway/internal/client"
	"example.com/order-system/services/gateway/internal/httputil"
)

// TokenValidator — интерфейс для валидации токенов.
// Позволяет мокировать в тестах вместо реального gRPC клиента.
type TokenValidator interface {
	ValidateToken(ctx context.Context, accessToken string) (*client.TokenInfo, error)
}

// AuthMiddleware — middleware для проверки JWT токенов.
// Валидация происходит через gRPC вызов к User Service,
// который проверяет подпись, срок действия и blacklist.
type AuthMiddleware struct {
	tokenValidator TokenValidator
}

// NewAuthMiddleware создаёт новый middleware для аутентификации.
// Принимает TokenValidator (обычно *client.UserClient) для валидации токенов.
func NewAuthMiddleware(tokenValidator TokenValidator) *AuthMiddleware {
	return &AuthMiddleware{
		tokenValidator: tokenValidator,
	}
}

// Handle возвращает Gin handler function для middleware.
func (m *AuthMiddleware) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		log := logger.FromContext(ctx)

		// Извлекаем токен из Authorization header.
		token := httputil.ExtractBearerToken(c)
		if token == "" {
			log.Debug().Msg("Отсутствует токен авторизации")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "Требуется авторизация",
			})
			return
		}

		// Валидация токена через User Service
		tokenInfo, err := m.tokenValidator.ValidateToken(ctx, token)
		if err != nil {
			log.Warn().Err(err).Msg("Ошибка валидации токена")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "Невалидный токен",
			})
			return
		}

		// Проверяем результат валидации
		if !tokenInfo.Valid {
			log.Debug().Msg("Токен невалиден или отозван")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "Токен недействителен",
			})
			return
		}

		// Сохраняем данные пользователя в контекст Gin
		c.Set("user_id", tokenInfo.UserID)
		c.Set("email", tokenInfo.Email)
		c.Set("jti", tokenInfo.JTI)

		log.Debug().
			Str("user_id", tokenInfo.UserID).
			Str("jti", tokenInfo.JTI).
			Msg("Пользователь аутентифицирован")

		c.Next()
	}
}
