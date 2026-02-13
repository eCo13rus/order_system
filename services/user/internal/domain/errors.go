// Package domain содержит бизнес-сущности и доменные ошибки User Service.
package domain

import "errors"

// Доменные ошибки User Service.
// Используются для передачи бизнес-ошибок между слоями приложения.
var (
	// ErrUserNotFound возвращается, когда пользователь не найден в базе данных.
	ErrUserNotFound = errors.New("пользователь не найден")

	// ErrEmailExists возвращается при попытке регистрации с уже занятым email.
	ErrEmailExists = errors.New("пользователь с таким email уже существует")

	// ErrInvalidCredentials возвращается при неверном email или пароле.
	ErrInvalidCredentials = errors.New("неверный email или пароль")

	// ErrInvalidToken возвращается при невалидном или просроченном токене.
	ErrInvalidToken = errors.New("невалидный или просроченный токен")

	// ErrTokenRevoked возвращается, когда токен был отозван (logout).
	ErrTokenRevoked = errors.New("токен был отозван")

	// ErrWeakPassword возвращается, если пароль не соответствует требованиям безопасности.
	ErrWeakPassword = errors.New("пароль должен содержать минимум 8 символов")

	// ErrInvalidEmail возвращается при некорректном формате email.
	ErrInvalidEmail = errors.New("некорректный формат email")

	// ErrEmptyName возвращается, если имя пользователя пустое.
	ErrEmptyName = errors.New("имя пользователя не может быть пустым")

	// ErrAccountLocked возвращается, когда аккаунт заблокирован из-за множества неудачных попыток входа.
	ErrAccountLocked = errors.New("аккаунт временно заблокирован, попробуйте позже")
)
