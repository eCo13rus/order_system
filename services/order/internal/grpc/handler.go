// Package grpc содержит gRPC обработчики для Order Service.
package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
	commonv1 "example.com/order-system/proto/common/v1"
	orderv1 "example.com/order-system/proto/order/v1"
	"example.com/order-system/services/order/internal/domain"
	"example.com/order-system/services/order/internal/service"
)

// Handler реализует gRPC интерфейс OrderServiceServer.
type Handler struct {
	orderv1.UnimplementedOrderServiceServer
	orderService service.OrderService
}

// NewHandler создаёт новый gRPC обработчик.
func NewHandler(orderService service.OrderService) *Handler {
	return &Handler{
		orderService: orderService,
	}
}

// CreateOrder создаёт новый заказ.
func (h *Handler) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id обязателен")
	}
	if len(req.GetItems()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "заказ должен содержать хотя бы одну позицию")
	}

	// Конвертация proto OrderItem в domain.OrderItem
	domainItems := make([]domain.OrderItem, 0, len(req.GetItems()))
	for _, item := range req.GetItems() {
		domainItem, err := protoItemToDomain(item)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "некорректная позиция заказа: %v", err)
		}
		domainItems = append(domainItems, domainItem)
	}

	// Вызов бизнес-логики
	order, err := h.orderService.CreateOrder(ctx, req.GetUserId(), req.GetIdempotencyKey(), domainItems)
	if err != nil {
		return nil, h.mapError(ctx, err, "CreateOrder")
	}

	log.Info().
		Str("order_id", order.ID).
		Str("user_id", order.UserID).
		Int("items_count", len(order.Items)).
		Msg("gRPC: заказ успешно создан")

	return &orderv1.CreateOrderResponse{
		OrderId: order.ID,
		Status:  domainStatusToProto(order.Status),
	}, nil
}

// GetOrder возвращает заказ по идентификатору.
func (h *Handler) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id обязателен")
	}

	// Вызов бизнес-логики
	order, err := h.orderService.GetOrder(ctx, req.GetOrderId())
	if err != nil {
		return nil, h.mapError(ctx, err, "GetOrder")
	}

	log.Debug().
		Str("order_id", order.ID).
		Msg("gRPC: заказ получен")

	return &orderv1.GetOrderResponse{
		Order: domainOrderToProto(order),
	}, nil
}

// ListOrders возвращает список заказов пользователя с пагинацией.
func (h *Handler) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id обязателен")
	}

	// Извлечение параметров пагинации
	var page, pageSize int32 = 1, 20
	if req.GetPagination() != nil {
		if req.GetPagination().GetPage() > 0 {
			page = req.GetPagination().GetPage()
		}
		if req.GetPagination().GetPageSize() > 0 {
			pageSize = req.GetPagination().GetPageSize()
		}
	}

	// Опциональный фильтр по статусу
	var statusFilter *domain.OrderStatus
	if req.StatusFilter != nil {
		domainStatus := protoStatusToDomain(req.GetStatusFilter())
		// Игнорируем UNSPECIFIED статус — он означает "без фильтра"
		if domainStatus != "" {
			statusFilter = &domainStatus
		}
	}

	// Вызов бизнес-логики
	orders, total, err := h.orderService.ListOrders(ctx, req.GetUserId(), statusFilter, int(page), int(pageSize))
	if err != nil {
		return nil, h.mapError(ctx, err, "ListOrders")
	}

	// Конвертация списка заказов в proto
	protoOrders := make([]*orderv1.Order, 0, len(orders))
	for _, order := range orders {
		protoOrders = append(protoOrders, domainOrderToProto(order))
	}

	// Расчёт общего количества страниц
	totalPages := int32(total) / pageSize
	if int32(total)%pageSize > 0 {
		totalPages++
	}

	log.Debug().
		Str("user_id", req.GetUserId()).
		Int("page", int(page)).
		Int("page_size", int(pageSize)).
		Int64("total", total).
		Int("returned", len(orders)).
		Msg("gRPC: список заказов получен")

	return &orderv1.ListOrdersResponse{
		Orders: protoOrders,
		PaginationMeta: &commonv1.PaginationMeta{
			CurrentPage: page,
			PageSize:    pageSize,
			TotalItems:  total,
			TotalPages:  totalPages,
		},
	}, nil
}

// CancelOrder отменяет заказ.
func (h *Handler) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	log := logger.FromContext(ctx)

	// Валидация входных данных
	if req.GetOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id обязателен")
	}

	// Вызов бизнес-логики
	err := h.orderService.CancelOrder(ctx, req.GetOrderId())
	if err != nil {
		return nil, h.mapError(ctx, err, "CancelOrder")
	}

	log.Info().
		Str("order_id", req.GetOrderId()).
		Msg("gRPC: заказ успешно отменён")

	return &orderv1.CancelOrderResponse{
		Success: true,
		Message: "заказ успешно отменён",
	}, nil
}

// domainOrderToProto конвертирует доменную сущность Order в proto сообщение.
func domainOrderToProto(order *domain.Order) *orderv1.Order {
	if order == nil {
		return nil
	}

	protoOrder := &orderv1.Order{
		Id:          order.ID,
		UserId:      order.UserID,
		Items:       make([]*orderv1.OrderItem, 0, len(order.Items)),
		TotalAmount: domainMoneyToProto(order.TotalAmount),
		Status:      domainStatusToProto(order.Status),
		CreatedAt:   order.CreatedAt.Unix(),
		UpdatedAt:   order.UpdatedAt.Unix(),
	}

	// Конвертация позиций заказа
	for i := range order.Items {
		protoOrder.Items = append(protoOrder.Items, domainItemToProto(&order.Items[i]))
	}

	// Опциональные поля
	if order.PaymentID != nil {
		protoOrder.PaymentId = order.PaymentID
	}
	if order.FailureReason != nil {
		protoOrder.FailureReason = order.FailureReason
	}

	return protoOrder
}

// domainItemToProto конвертирует доменную сущность OrderItem в proto сообщение.
func domainItemToProto(item *domain.OrderItem) *orderv1.OrderItem {
	if item == nil {
		return nil
	}

	return &orderv1.OrderItem{
		ProductId:   item.ProductID,
		ProductName: item.ProductName,
		Quantity:    item.Quantity,
		UnitPrice:   domainMoneyToProto(item.UnitPrice),
	}
}

// domainMoneyToProto конвертирует доменную сущность Money в proto сообщение.
func domainMoneyToProto(money domain.Money) *commonv1.Money {
	return &commonv1.Money{
		Amount:   money.Amount,
		Currency: money.Currency,
	}
}

// protoItemToDomain конвертирует proto OrderItem в доменную сущность.
func protoItemToDomain(item *orderv1.OrderItem) (domain.OrderItem, error) {
	if item == nil {
		return domain.OrderItem{}, errors.New("позиция заказа не может быть nil")
	}

	var unitPrice domain.Money
	if item.GetUnitPrice() != nil {
		unitPrice = domain.Money{
			Amount:   item.GetUnitPrice().GetAmount(),
			Currency: item.GetUnitPrice().GetCurrency(),
		}
	}

	return domain.OrderItem{
		ProductID:   item.GetProductId(),
		ProductName: item.GetProductName(),
		Quantity:    item.GetQuantity(),
		UnitPrice:   unitPrice,
	}, nil
}

// domainStatusToProto конвертирует доменный статус заказа в proto enum.
func domainStatusToProto(status domain.OrderStatus) orderv1.OrderStatus {
	switch status {
	case domain.OrderStatusPending:
		return orderv1.OrderStatus_ORDER_STATUS_PENDING
	case domain.OrderStatusConfirmed:
		return orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
	case domain.OrderStatusCancelled:
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	case domain.OrderStatusFailed:
		return orderv1.OrderStatus_ORDER_STATUS_FAILED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

// protoStatusToDomain конвертирует proto enum статуса в доменный статус.
func protoStatusToDomain(status orderv1.OrderStatus) domain.OrderStatus {
	switch status {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING:
		return domain.OrderStatusPending
	case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED:
		return domain.OrderStatusConfirmed
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return domain.OrderStatusCancelled
	case orderv1.OrderStatus_ORDER_STATUS_FAILED:
		return domain.OrderStatusFailed
	default:
		// UNSPECIFIED означает "без фильтра"
		return ""
	}
}

// mapError преобразует доменные ошибки в gRPC статусы.
func (h *Handler) mapError(ctx context.Context, err error, method string) error {
	log := logger.FromContext(ctx)

	// Маппинг доменных ошибок на gRPC коды
	switch {
	case errors.Is(err, domain.ErrOrderNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, domain.ErrDuplicateOrder):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, domain.ErrOrderCannotCancel):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, domain.ErrInvalidUserID):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrEmptyOrderItems):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrInvalidQuantity):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrInvalidProductID):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrInvalidProductName):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrInvalidPrice):
		return status.Error(codes.InvalidArgument, err.Error())

	default:
		// Внутренняя ошибка сервера
		log.Error().
			Err(err).
			Str("method", method).
			Msg("gRPC: внутренняя ошибка")
		return status.Error(codes.Internal, "внутренняя ошибка сервера")
	}
}
