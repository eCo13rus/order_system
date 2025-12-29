// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/order-system/pkg/logger"
)

// UserHandler — обработчик пользователей.
type UserHandler struct {
	userService UserService
}

// NewUserHandler создаёт новый обработчик пользователей.
// Принимает интерфейс UserService для возможности мокирования в тестах.
func NewUserHandler(userService UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

// UserResponse — информация о пользователе.
type UserResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// GetMe возвращает информацию о текущем пользователе.
// GET /api/v1/users/me
// Требует авторизации (user_id из JWT в контексте).
func (h *UserHandler) GetMe(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	// user_id устанавливается JWT middleware
	userID, exists := c.Get("user_id")
	if !exists {
		log.Warn().Msg("user_id не найден в контексте")
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "Требуется авторизация",
		})
		return
	}

	// Defensive: проверяем тип (middleware гарантирует string, но panic недопустим)
	userIDStr, ok := userID.(string)
	if !ok {
		log.Error().Interface("user_id", userID).Msg("user_id не является строкой — баг в middleware")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Внутренняя ошибка сервера",
		})
		return
	}

	user, err := h.userService.GetUser(ctx, userIDStr)
	if err != nil {
		HandleGRPCError(c, err, "GetMe")
		return
	}

	c.JSON(http.StatusOK, UserResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	})
}

// GetUser возвращает информацию о пользователе по ID.
// GET /api/v1/users/:id
// Требует авторизации.
func (h *UserHandler) GetUser(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "ID пользователя обязателен",
		})
		return
	}

	user, err := h.userService.GetUser(ctx, userID)
	if err != nil {
		HandleGRPCError(c, err, "GetUser")
		return
	}

	c.JSON(http.StatusOK, UserResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	})
}
