// Package httputil содержит вспомогательные функции для HTTP обработки.
package httputil

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// ExtractBearerToken извлекает токен из Authorization header.
// Формат: "Bearer <token>"
// Поддерживает регистронезависимый префикс и обрезает пробелы.
func ExtractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		return ""
	}

	// Проверяем формат "Bearer <token>".
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}

	return strings.TrimSpace(parts[1])
}
