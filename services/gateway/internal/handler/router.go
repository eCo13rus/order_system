// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"example.com/order-system/pkg/metrics"
	"example.com/order-system/services/gateway/internal/client"
	"example.com/order-system/services/gateway/internal/middleware"
)

// ReadinessChecker — функция проверки готовности сервиса.
type ReadinessChecker func(ctx context.Context) error

// Router — конфигурация роутера.
type Router struct {
	engine         *gin.Engine
	userClient     *client.UserClient
	orderClient    *client.OrderClient
	authMW         *middleware.AuthMiddleware
	rateLimitMW    *middleware.RateLimitMiddleware
	tracingMW      *middleware.TracingMiddleware
	readinessCheck ReadinessChecker // опциональная проверка готовности
}

// RouterConfig — параметры для создания роутера.
type RouterConfig struct {
	UserClient     *client.UserClient
	OrderClient    *client.OrderClient
	AuthMW         *middleware.AuthMiddleware
	RateLimitMW    *middleware.RateLimitMiddleware
	TracingMW      *middleware.TracingMiddleware
	ReadinessCheck ReadinessChecker // опциональная проверка готовности для /readyz
	Debug          bool             // Режим отладки Gin
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

	// CORS — обработка cross-origin запросов
	engine.Use(middleware.CORS(middleware.DefaultCORSConfig()))

	// Security headers — защита от clickjacking, MIME-sniffing, XSS
	engine.Use(middleware.SecurityHeaders())

	// OpenTelemetry tracing — создаёт spans для Jaeger
	engine.Use(otelgin.Middleware("gateway"))

	// Prometheus метрики — requests_total, request_duration_seconds
	engine.Use(metrics.GinMetricsMiddleware("gateway"))

	r := &Router{
		engine:         engine,
		userClient:     cfg.UserClient,
		orderClient:    cfg.OrderClient,
		authMW:         cfg.AuthMW,
		rateLimitMW:    cfg.RateLimitMW,
		tracingMW:      cfg.TracingMW,
		readinessCheck: cfg.ReadinessCheck,
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

	// Health endpoints (без rate limiting и auth)
	r.engine.GET("/health", r.healthCheck)           // legacy, оставлен для совместимости
	r.engine.GET("/healthz", r.livenessCheck)        // k3s liveness probe
	r.engine.GET("/readyz", r.readinessCheckHandler) // k3s readiness probe

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

	// === Order routes (защищённые) ===
	if r.orderClient != nil {
		orderHandler := NewOrderHandler(r.orderClient)
		orders := v1.Group("/orders")
		if r.authMW != nil {
			orders.Use(r.authMW.Handle())
		}
		{
			orders.POST("", orderHandler.CreateOrder)
			orders.GET("", orderHandler.ListOrders)
			orders.GET("/:id", orderHandler.GetOrder)
			orders.DELETE("/:id", orderHandler.CancelOrder)
		}
	}
}

// Engine возвращает Gin engine для запуска сервера.
func (r *Router) Engine() *gin.Engine {
	return r.engine
}

// healthCheck — проверка работоспособности сервиса (legacy).
func (r *Router) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "api-gateway",
	})
}

// livenessCheck — liveness probe для Kubernetes.
// Возвращает 200 OK если процесс жив (сервер отвечает = процесс работает).
func (r *Router) livenessCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// readinessCheckHandler — readiness probe для Kubernetes.
// Возвращает 200 OK если сервис готов принимать трафик (все зависимости доступны).
func (r *Router) readinessCheckHandler(c *gin.Context) {
	// Если ReadinessChecker не установлен — считаем сервис готовым
	if r.readinessCheck == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
		return
	}

	// Проверяем готовность с таймаутом 5 секунд
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := r.readinessCheck(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
