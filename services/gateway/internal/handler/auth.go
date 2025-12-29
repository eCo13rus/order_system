// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/gateway/internal/httputil"
)

// AuthHandler — обработчик аутентификации.
type AuthHandler struct {
	userService UserService
}

// NewAuthHandler создаёт новый обработчик аутентификации.
// Принимает интерфейс UserService для возможности мокирования в тестах.
func NewAuthHandler(userService UserService) *AuthHandler {
	return &AuthHandler{
		userService: userService,
	}
}

// RegisterRequest — запрос на регистрацию.
type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	Name     string `json:"name" binding:"required,min=2"`
}

// RegisterResponse — ответ на регистрацию.
type RegisterResponse struct {
	UserID string `json:"user_id"`
}

// Register регистрирует нового пользователя.
// POST /api/v1/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Debug().Err(err).Msg("Невалидный запрос на регистрацию")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Невалидные данные запроса",
		})
		return
	}

	userID, err := h.userService.Register(ctx, req.Email, req.Password, req.Name)
	if err != nil {
		HandleGRPCError(c, err, "Register")
		return
	}

	log.Info().
		Str("user_id", userID).
		Str("email", req.Email).
		Msg("Пользователь зарегистрирован")

	c.JSON(http.StatusCreated, RegisterResponse{
		UserID: userID,
	})
}

// LoginRequest — запрос на вход.
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse — ответ на вход.
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

// Login аутентифицирует пользователя.
// POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Debug().Err(err).Msg("Невалидный запрос на вход")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Невалидные данные запроса",
		})
		return
	}

	result, err := h.userService.Login(ctx, req.Email, req.Password)
	if err != nil {
		HandleGRPCError(c, err, "Login")
		return
	}

	log.Info().
		Str("email", req.Email).
		Msg("Пользователь вошёл в систему")

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    result.ExpiresAt,
	})
}

// Logout выход из системы.
// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	// Извлекаем токен из Authorization header.
	token := httputil.ExtractBearerToken(c)
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "Отсутствует токен авторизации",
		})
		return
	}

	if err := h.userService.Logout(ctx, token); err != nil {
		HandleGRPCError(c, err, "Logout")
		return
	}

	log.Info().Msg("Пользователь вышел из системы")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}
