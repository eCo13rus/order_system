// Package middleware — Security Headers middleware для защиты от типовых веб-атак.
package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders добавляет заголовки безопасности ко всем ответам.
// Защищает от: clickjacking (X-Frame-Options), MIME-sniffing (X-Content-Type-Options),
// XSS (X-XSS-Protection), информационной утечки (X-Powered-By).
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()

		// Запрет встраивания в iframe — защита от clickjacking
		h.Set("X-Frame-Options", "DENY")

		// Запрет MIME-type sniffing — браузер не будет "угадывать" тип контента
		h.Set("X-Content-Type-Options", "nosniff")

		// Включаем встроенный XSS-фильтр браузера
		h.Set("X-XSS-Protection", "1; mode=block")

		// Скрываем информацию о сервере
		h.Set("X-Powered-By", "")

		// Запрет кеширования для API ответов (токены, данные пользователей)
		h.Set("Cache-Control", "no-store")

		// Referrer Policy — не отправлять referrer на другие домены
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions Policy — отключаем ненужные браузерные API
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		c.Next()
	}
}
