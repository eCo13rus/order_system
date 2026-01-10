// Package domain содержит бизнес-сущности и доменные ошибки Order Service.
package domain

import "errors"

// Доменные ошибки Order Service.
// Используются для передачи бизнес-ошибок между слоями приложения.
var (
	// ErrOrderNotFound возвращается, когда заказ не найден в базе данных.
	ErrOrderNotFound = errors.New("заказ не найден")

	// ErrEmptyOrderItems возвращается при попытке создать заказ без позиций.
	ErrEmptyOrderItems = errors.New("заказ должен содержать хотя бы одну позицию")

	// ErrInvalidUserID возвращается при пустом или некорректном идентификаторе пользователя.
	ErrInvalidUserID = errors.New("некорректный идентификатор пользователя")

	// ErrInvalidProductID возвращается при пустом или некорректном идентификаторе товара.
	ErrInvalidProductID = errors.New("некорректный идентификатор товара")

	// ErrInvalidProductName возвращается при пустом названии товара.
	ErrInvalidProductName = errors.New("название товара не может быть пустым")

	// ErrInvalidQuantity возвращается, когда количество товара меньше или равно нулю.
	ErrInvalidQuantity = errors.New("количество должно быть больше нуля")

	// ErrInvalidPrice возвращается, когда цена товара меньше или равна нулю.
	ErrInvalidPrice = errors.New("цена должна быть больше нуля")

	// ErrOrderCannotCancel возвращается при попытке отменить заказ в неподходящем статусе.
	ErrOrderCannotCancel = errors.New("заказ нельзя отменить в текущем статусе")

	// ErrOrderCannotConfirm возвращается при попытке подтвердить заказ в неподходящем статусе.
	ErrOrderCannotConfirm = errors.New("заказ нельзя подтвердить в текущем статусе")

	// ErrOrderCannotFail возвращается при попытке пометить заказ как failed в неподходящем статусе.
	ErrOrderCannotFail = errors.New("заказ нельзя пометить как failed в текущем статусе")

	// ErrDuplicateOrder возвращается при попытке создать заказ с уже существующим idempotency_key.
	ErrDuplicateOrder = errors.New("заказ с таким idempotency_key уже существует")
)
