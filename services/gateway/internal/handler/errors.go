// Package handler содержит HTTP обработчики для REST API.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"example.com/order-system/pkg/logger"
)

// ErrorResponse — стандартный формат ошибки API.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// HandleGRPCError преобразует gRPC ошибку в HTTP ответ.
// Используется всеми handlers для единообразной обработки ошибок.
// ВАЖНО: err не должен быть nil — это баг в вызывающем коде.
func HandleGRPCError(c *gin.Context, err error, method string) {
	// Guard: nil ошибка — баг в вызывающем коде, логируем и возвращаем 500.
	if err == nil {
		logger.Error().Str("method", method).Msg("HandleGRPCError вызван с nil ошибкой — баг в коде")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Внутренняя ошибка сервера",
		})
		return
	}

	log := logger.FromContext(c.Request.Context())

	st, ok := status.FromError(err)
	if !ok {
		log.Error().Err(err).Str("method", method).Msg("Внутренняя ошибка")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Внутренняя ошибка сервера",
		})
		return
	}

	// Маппинг gRPC кодов в HTTP статусы.
	var httpStatus int
	var errorCode string

	switch st.Code() {
	case codes.InvalidArgument:
		httpStatus = http.StatusBadRequest
		errorCode = "invalid_argument"
	case codes.NotFound:
		httpStatus = http.StatusNotFound
		errorCode = "not_found"
	case codes.AlreadyExists:
		httpStatus = http.StatusConflict
		errorCode = "already_exists"
	case codes.Unauthenticated:
		httpStatus = http.StatusUnauthorized
		errorCode = "unauthenticated"
	case codes.PermissionDenied:
		httpStatus = http.StatusForbidden
		errorCode = "permission_denied"
	case codes.FailedPrecondition:
		httpStatus = http.StatusConflict
		errorCode = "failed_precondition"
	case codes.Unavailable:
		httpStatus = http.StatusServiceUnavailable
		errorCode = "service_unavailable"
	default:
		httpStatus = http.StatusInternalServerError
		errorCode = "internal_error"
		log.Error().
			Err(err).
			Str("method", method).
			Str("grpc_code", st.Code().String()).
			Msg("Ошибка gRPC")
	}

	c.JSON(httpStatus, ErrorResponse{
		Error:   errorCode,
		Message: st.Message(),
	})
}
