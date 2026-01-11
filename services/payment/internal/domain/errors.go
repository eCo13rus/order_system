// Package domain содержит бизнес-сущности Payment Service.
package domain

import "errors"

// Доменные ошибки Payment Service.
var (
	// ErrPaymentNotFound — платёж не найден.
	ErrPaymentNotFound = errors.New("платёж не найден")

	// ErrPaymentCompleted — платёж уже завершён.
	ErrPaymentCompleted = errors.New("платёж уже завершён")

	// ErrInvalidTransition — недопустимый переход состояния.
	ErrInvalidTransition = errors.New("недопустимый переход состояния платежа")

	// ErrInvalidAmount — некорректная сумма платежа.
	ErrInvalidAmount = errors.New("сумма платежа должна быть больше нуля")

	// ErrDuplicatePayment — платёж с таким idempotency_key уже существует.
	ErrDuplicatePayment = errors.New("платёж с таким ключом идемпотентности уже существует")

	// ErrInsufficientFunds — недостаточно средств (симуляция отклонения).
	ErrInsufficientFunds = errors.New("недостаточно средств для оплаты")

	// ErrPaymentDeclined — платёж отклонён провайдером.
	ErrPaymentDeclined = errors.New("платёж отклонён")
)
