// Package client содержит gRPC клиенты для взаимодействия с микросервисами.
package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
	"example.com/order-system/pkg/middleware"
	commonv1 "example.com/order-system/proto/common/v1"
	orderv1 "example.com/order-system/proto/order/v1"
)

// OrderClient — клиент для взаимодействия с Order Service.
type OrderClient struct {
	conn   *grpc.ClientConn
	client orderv1.OrderServiceClient
}

// OrderClientConfig — конфигурация клиента.
type OrderClientConfig struct {
	Addr    string        // Адрес Order Service (host:port)
	Timeout time.Duration // Таймаут подключения
}

// NewOrderClient создаёт новый gRPC клиент к Order Service.
func NewOrderClient(cfg OrderClientConfig) (*OrderClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}

	// Создаём соединение через grpc.NewClient (современный API).
	conn, err := grpc.NewClient(cfg.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания gRPC клиента (%s): %w", cfg.Addr, err)
	}

	// Проверяем доступность сервиса с таймаутом.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	client := orderv1.NewOrderServiceClient(conn)

	// Пробуем пинг — если сервис недоступен, узнаем сразу.
	// GetOrder с пустым ID — лёгкий способ проверить связь.
	_, err = client.GetOrder(ctx, &orderv1.GetOrderRequest{OrderId: ""})
	// Ошибка gRPC Unavailable означает, что сервис недоступен.
	// Другие ошибки (InvalidArgument, NotFound) означают, что связь есть.
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			_ = conn.Close() // Игнорируем ошибку закрытия — уже есть ошибка подключения.
			return nil, fmt.Errorf("Order Service недоступен (%s): %w", cfg.Addr, err)
		}
	}

	logger.Info().
		Str("addr", cfg.Addr).
		Msg("Подключено к Order Service")

	return &OrderClient{
		conn:   conn,
		client: client,
	}, nil
}

// Close закрывает соединение с Order Service.
func (c *OrderClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// === DTO для REST API (без proto зависимостей в handler) ===

// Order — информация о заказе.
type Order struct {
	ID            string      `json:"id"`
	UserID        string      `json:"user_id"`
	Items         []OrderItem `json:"items"`
	TotalAmount   Money       `json:"total_amount"`
	Status        string      `json:"status"`
	PaymentID     *string     `json:"payment_id,omitempty"`
	FailureReason *string     `json:"failure_reason,omitempty"`
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at"`
}

// OrderItem — позиция заказа.
type OrderItem struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int32  `json:"quantity"`
	UnitPrice   Money  `json:"unit_price"`
}

// Money — денежная сумма.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// ListOrdersResult — результат запроса списка заказов.
type ListOrdersResult struct {
	Orders     []Order `json:"orders"`
	Total      int64   `json:"total"`
	TotalPages int     `json:"total_pages"`
}

// CreateOrderResult — результат создания заказа.
type CreateOrderResult struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

// === Методы клиента ===

// CreateOrder создаёт новый заказ.
func (c *OrderClient) CreateOrder(ctx context.Context, userID, idempotencyKey string, items []OrderItem) (*CreateOrderResult, error) {
	ctx = middleware.InjectTraceMetadata(ctx)

	// Преобразуем DTO в proto
	protoItems := make([]*orderv1.OrderItem, len(items))
	for i, item := range items {
		protoItems[i] = &orderv1.OrderItem{
			ProductId:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice: &commonv1.Money{
				Amount:   item.UnitPrice.Amount,
				Currency: item.UnitPrice.Currency,
			},
		}
	}

	resp, err := c.client.CreateOrder(ctx, &orderv1.CreateOrderRequest{
		UserId:         userID,
		Items:          protoItems,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}

	return &CreateOrderResult{
		OrderID: resp.GetOrderId(),
		Status:  orderStatusToString(resp.GetStatus()),
	}, nil
}

// GetOrder возвращает заказ по ID.
func (c *OrderClient) GetOrder(ctx context.Context, orderID string) (*Order, error) {
	ctx = middleware.InjectTraceMetadata(ctx)

	resp, err := c.client.GetOrder(ctx, &orderv1.GetOrderRequest{
		OrderId: orderID,
	})
	if err != nil {
		return nil, err
	}

	return protoOrderToDTO(resp.GetOrder()), nil
}

// ListOrders возвращает список заказов пользователя.
func (c *OrderClient) ListOrders(ctx context.Context, userID string, status *string, page, pageSize int) (*ListOrdersResult, error) {
	ctx = middleware.InjectTraceMetadata(ctx)

	req := &orderv1.ListOrdersRequest{
		UserId: userID,
		Pagination: &commonv1.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}

	// Добавляем фильтр по статусу, если указан
	if status != nil {
		statusEnum := stringToOrderStatus(*status)
		req.StatusFilter = &statusEnum
	}

	resp, err := c.client.ListOrders(ctx, req)
	if err != nil {
		return nil, err
	}

	orders := make([]Order, len(resp.GetOrders()))
	for i, o := range resp.GetOrders() {
		orders[i] = *protoOrderToDTO(o)
	}

	meta := resp.GetPaginationMeta()
	return &ListOrdersResult{
		Orders:     orders,
		Total:      meta.GetTotalItems(),
		TotalPages: int(meta.GetTotalPages()),
	}, nil
}

// CancelOrder отменяет заказ.
func (c *OrderClient) CancelOrder(ctx context.Context, orderID string) error {
	ctx = middleware.InjectTraceMetadata(ctx)

	resp, err := c.client.CancelOrder(ctx, &orderv1.CancelOrderRequest{
		OrderId: orderID,
	})
	if err != nil {
		return err
	}

	if !resp.GetSuccess() {
		return fmt.Errorf("не удалось отменить заказ: %s", resp.GetMessage())
	}

	return nil
}

// === Вспомогательные функции ===

// protoOrderToDTO преобразует proto Order в DTO.
func protoOrderToDTO(o *orderv1.Order) *Order {
	if o == nil {
		return nil
	}

	items := make([]OrderItem, len(o.GetItems()))
	for i, item := range o.GetItems() {
		items[i] = OrderItem{
			ProductID:   item.GetProductId(),
			ProductName: item.GetProductName(),
			Quantity:    item.GetQuantity(),
			UnitPrice: Money{
				Amount:   item.GetUnitPrice().GetAmount(),
				Currency: item.GetUnitPrice().GetCurrency(),
			},
		}
	}

	order := &Order{
		ID:     o.GetId(),
		UserID: o.GetUserId(),
		Items:  items,
		TotalAmount: Money{
			Amount:   o.GetTotalAmount().GetAmount(),
			Currency: o.GetTotalAmount().GetCurrency(),
		},
		Status:    orderStatusToString(o.GetStatus()),
		CreatedAt: o.GetCreatedAt(),
		UpdatedAt: o.GetUpdatedAt(),
	}

	// Опциональные поля
	if o.PaymentId != nil {
		paymentID := o.GetPaymentId()
		order.PaymentID = &paymentID
	}
	if o.FailureReason != nil {
		failureReason := o.GetFailureReason()
		order.FailureReason = &failureReason
	}

	return order
}

// orderStatusToString преобразует enum статуса в строку.
func orderStatusToString(s orderv1.OrderStatus) string {
	switch s {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING:
		return "PENDING"
	case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED:
		return "CONFIRMED"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "CANCELLED"
	case orderv1.OrderStatus_ORDER_STATUS_FAILED:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// stringToOrderStatus преобразует строку в enum статуса.
func stringToOrderStatus(s string) orderv1.OrderStatus {
	switch s {
	case "PENDING":
		return orderv1.OrderStatus_ORDER_STATUS_PENDING
	case "CONFIRMED":
		return orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
	case "CANCELLED":
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	case "FAILED":
		return orderv1.OrderStatus_ORDER_STATUS_FAILED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}
