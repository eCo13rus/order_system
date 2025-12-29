// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"github.com/gin-gonic/gin"

	"example.com/order-system/services/gateway/internal/client"
	"example.com/order-system/services/gateway/internal/middleware"
)

// Router — конфигурация роутера.
type Router struct {
	engine      *gin.Engine
	userClient  *client.UserClient
	authMW      *middleware.AuthMiddleware
	rateLimitMW *middleware.RateLimitMiddleware
	tracingMW   *middleware.TracingMiddleware
}

// RouterConfig — параметры для создания роутера.
type RouterConfig struct {
	UserClient  *client.UserClient
	AuthMW      *middleware.AuthMiddleware
	RateLimitMW *middleware.RateLimitMiddleware
	TracingMW   *middleware.TracingMiddleware
	Debug       bool // Режим отладки Gin
}

// NewRouter создаёт и настраивает HTTP роутер.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()

	// Стандартные middleware Gin
	engine.Use(gin.Recovery())

	r := &Router{
		engine:      engine,
		userClient:  cfg.UserClient,
		authMW:      cfg.AuthMW,
		rateLimitMW: cfg.RateLimitMW,
		tracingMW:   cfg.TracingMW,
	}

	r.setupRoutes()
	return r
}

// setupRoutes настраивает все маршруты API.
func (r *Router) setupRoutes() {
	// Глобальные middleware
	if r.tracingMW != nil {
		r.engine.Use(r.tracingMW.Handle())
	}

	// Health check (без rate limiting и auth)
	r.engine.GET("/health", r.healthCheck)

	// API v1
	v1 := r.engine.Group("/api/v1")

	// Rate limiting на уровне API (если включен)
	if r.rateLimitMW != nil {
		v1.Use(r.rateLimitMW.Handle())
	}

	// === Auth routes (публичные) ===
	authHandler := NewAuthHandler(r.userClient)
	auth := v1.Group("/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
		auth.POST("/logout", authHandler.Logout) // Требует токен, но не проверяет валидность
	}

	// === User routes (защищённые) ===
	userHandler := NewUserHandler(r.userClient)
	users := v1.Group("/users")
	if r.authMW != nil {
		users.Use(r.authMW.Handle())
	}
	{
		users.GET("/me", userHandler.GetMe)
		users.GET("/:id", userHandler.GetUser)
	}
}

// Engine возвращает Gin engine для запуска сервера.
func (r *Router) Engine() *gin.Engine {
	return r.engine
}

// healthCheck — проверка работоспособности сервиса.
func (r *Router) healthCheck(c *gin.Context) {
	c.JSON(200, gin.H{
		"status":  "ok",
		"service": "api-gateway",
	})
}
