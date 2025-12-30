// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/gateway/internal/client"
)

// OrderHandler — обработчик заказов.
type OrderHandler struct {
	orderService OrderService
}

// NewOrderHandler создаёт новый обработчик заказов.
func NewOrderHandler(orderService OrderService) *OrderHandler {
	return &OrderHandler{
		orderService: orderService,
	}
}

// === Request/Response DTOs ===

// CreateOrderRequest — запрос на создание заказа.
type CreateOrderRequest struct {
	Items          []CreateOrderItemRequest `json:"items" binding:"required,min=1,dive"`
	IdempotencyKey string                   `json:"idempotency_key" binding:"required"`
}

// CreateOrderItemRequest — позиция в запросе на создание заказа.
type CreateOrderItemRequest struct {
	ProductID   string       `json:"product_id" binding:"required,uuid"`
	ProductName string       `json:"product_name" binding:"required,min=1"`
	Quantity    int32        `json:"quantity" binding:"required,min=1"`
	UnitPrice   MoneyRequest `json:"unit_price" binding:"required"`
}

// MoneyRequest — денежная сумма в запросе.
type MoneyRequest struct {
	Amount   int64  `json:"amount" binding:"required,min=1"`
	Currency string `json:"currency" binding:"required,len=3"`
}

// CreateOrderResponse — ответ на создание заказа.
type CreateOrderResponse struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

// ListOrdersResponse — ответ на запрос списка заказов.
type ListOrdersResponse struct {
	Orders     []OrderResponse    `json:"orders"`
	Pagination PaginationResponse `json:"pagination"`
}

// PaginationResponse — информация о пагинации.
type PaginationResponse struct {
	CurrentPage int   `json:"current_page"`
	PageSize    int   `json:"page_size"`
	TotalItems  int64 `json:"total_items"`
	TotalPages  int   `json:"total_pages"`
}

// OrderResponse — информация о заказе в ответе.
type OrderResponse struct {
	ID            string              `json:"id"`
	UserID        string              `json:"user_id"`
	Items         []OrderItemResponse `json:"items"`
	TotalAmount   MoneyResponse       `json:"total_amount"`
	Status        string              `json:"status"`
	PaymentID     *string             `json:"payment_id,omitempty"`
	FailureReason *string             `json:"failure_reason,omitempty"`
	CreatedAt     int64               `json:"created_at"`
	UpdatedAt     int64               `json:"updated_at"`
}

// OrderItemResponse — позиция заказа в ответе.
type OrderItemResponse struct {
	ProductID   string        `json:"product_id"`
	ProductName string        `json:"product_name"`
	Quantity    int32         `json:"quantity"`
	UnitPrice   MoneyResponse `json:"unit_price"`
}

// MoneyResponse — денежная сумма в ответе.
type MoneyResponse struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// GetOrderResponse — ответ на запрос заказа.
type GetOrderResponse struct {
	Order OrderResponse `json:"order"`
}

// CancelOrderRequest удалён — DELETE не принимает body.
// failure_reason используется только для статуса FAILED (Saga).

// CancelOrderResponse — ответ на отмену заказа.
type CancelOrderResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// === Handlers ===

// CreateOrder создаёт новый заказ.
// POST /api/v1/orders
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	// Получаем user_id из JWT middleware
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Debug().Err(err).Msg("Невалидный запрос на создание заказа")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Невалидные данные запроса",
		})
		return
	}

	// Преобразуем request DTO в client DTO
	items := make([]client.OrderItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = client.OrderItem{
			ProductID:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice: client.Money{
				Amount:   item.UnitPrice.Amount,
				Currency: item.UnitPrice.Currency,
			},
		}
	}

	result, err := h.orderService.CreateOrder(ctx, userID, req.IdempotencyKey, items)
	if err != nil {
		HandleGRPCError(c, err, "CreateOrder")
		return
	}

	log.Info().
		Str("order_id", result.OrderID).
		Str("user_id", userID).
		Int("items_count", len(items)).
		Msg("Заказ создан")

	c.JSON(http.StatusCreated, CreateOrderResponse{
		OrderID: result.OrderID,
		Status:  result.Status,
	})
}

// ListOrders возвращает список заказов текущего пользователя.
// GET /api/v1/orders?page=1&page_size=20&status=PENDING
func (h *OrderHandler) ListOrders(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	// Получаем user_id из JWT middleware
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}

	// Парсим параметры пагинации
	page := 1
	pageSize := 20

	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	// Парсим фильтр по статусу
	var status *string
	if statusStr := c.Query("status"); statusStr != "" {
		// Валидируем статус
		validStatuses := map[string]bool{
			"PENDING":   true,
			"CONFIRMED": true,
			"CANCELLED": true,
			"FAILED":    true,
		}
		if validStatuses[statusStr] {
			status = &statusStr
		} else {
			log.Debug().Str("status", statusStr).Msg("Невалидный статус фильтра")
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "invalid_request",
				Message: "Невалидный статус: допустимые значения PENDING, CONFIRMED, CANCELLED, FAILED",
			})
			return
		}
	}

	result, err := h.orderService.ListOrders(ctx, userID, status, page, pageSize)
	if err != nil {
		HandleGRPCError(c, err, "ListOrders")
		return
	}

	// Преобразуем в response DTO
	orders := make([]OrderResponse, len(result.Orders))
	for i, o := range result.Orders {
		orders[i] = orderToResponse(&o)
	}

	log.Debug().
		Str("user_id", userID).
		Int("page", page).
		Int("count", len(orders)).
		Msg("Список заказов получен")

	c.JSON(http.StatusOK, ListOrdersResponse{
		Orders: orders,
		Pagination: PaginationResponse{
			CurrentPage: page,
			PageSize:    pageSize,
			TotalItems:  result.Total,
			TotalPages:  result.TotalPages,
		},
	})
}

// GetOrder возвращает заказ по ID.
// GET /api/v1/orders/:id
func (h *OrderHandler) GetOrder(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "ID заказа обязателен",
		})
		return
	}

	order, err := h.orderService.GetOrder(ctx, orderID)
	if err != nil {
		HandleGRPCError(c, err, "GetOrder")
		return
	}

	// Проверяем, что заказ принадлежит текущему пользователю
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}

	if order.UserID != userID {
		log.Warn().
			Str("order_id", orderID).
			Str("order_user_id", order.UserID).
			Str("request_user_id", userID).
			Msg("Попытка доступа к чужому заказу")
		c.JSON(http.StatusForbidden, ErrorResponse{
			Error:   "forbidden",
			Message: "Доступ к заказу запрещён",
		})
		return
	}

	c.JSON(http.StatusOK, GetOrderResponse{
		Order: orderToResponse(order),
	})
}

// CancelOrder отменяет заказ.
// DELETE /api/v1/orders/:id
// Body не принимается — failure_reason только для FAILED (Saga).
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	ctx := c.Request.Context()
	log := logger.FromContext(ctx)

	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "ID заказа обязателен",
		})
		return
	}

	// Получаем заказ для проверки владельца
	order, err := h.orderService.GetOrder(ctx, orderID)
	if err != nil {
		HandleGRPCError(c, err, "CancelOrder:GetOrder")
		return
	}

	// Проверяем, что заказ принадлежит текущему пользователю
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}

	if order.UserID != userID {
		log.Warn().
			Str("order_id", orderID).
			Str("order_user_id", order.UserID).
			Str("request_user_id", userID).
			Msg("Попытка отмены чужого заказа")
		c.JSON(http.StatusForbidden, ErrorResponse{
			Error:   "forbidden",
			Message: "Доступ к заказу запрещён",
		})
		return
	}

	if err := h.orderService.CancelOrder(ctx, orderID); err != nil {
		HandleGRPCError(c, err, "CancelOrder")
		return
	}

	log.Info().
		Str("order_id", orderID).
		Str("user_id", userID).
		Msg("Заказ отменён")

	c.JSON(http.StatusOK, CancelOrderResponse{
		Success: true,
		Message: "Заказ отменён",
	})
}

// === Helper functions ===

// getUserID извлекает user_id из контекста Gin.
// Возвращает false и отправляет ошибку, если user_id не найден.
func (h *OrderHandler) getUserID(c *gin.Context) (string, bool) {
	log := logger.FromContext(c.Request.Context())

	userID, exists := c.Get("user_id")
	if !exists {
		log.Warn().Msg("user_id не найден в контексте")
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "Требуется авторизация",
		})
		return "", false
	}

	// Defensive: проверяем тип
	userIDStr, ok := userID.(string)
	if !ok {
		log.Error().Interface("user_id", userID).Msg("user_id не является строкой — баг в middleware")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Внутренняя ошибка сервера",
		})
		return "", false
	}

	return userIDStr, true
}

// orderToResponse преобразует client.Order в OrderResponse.
func orderToResponse(o *client.Order) OrderResponse {
	items := make([]OrderItemResponse, len(o.Items))
	for i, item := range o.Items {
		items[i] = OrderItemResponse{
			ProductID:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice: MoneyResponse{
				Amount:   item.UnitPrice.Amount,
				Currency: item.UnitPrice.Currency,
			},
		}
	}

	return OrderResponse{
		ID:     o.ID,
		UserID: o.UserID,
		Items:  items,
		TotalAmount: MoneyResponse{
			Amount:   o.TotalAmount.Amount,
			Currency: o.TotalAmount.Currency,
		},
		Status:        o.Status,
		PaymentID:     o.PaymentID,
		FailureReason: o.FailureReason,
		CreatedAt:     o.CreatedAt,
		UpdatedAt:     o.UpdatedAt,
	}
}
