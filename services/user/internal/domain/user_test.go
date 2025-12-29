// Package domain содержит unit тесты для доменных сущностей User Service.
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUser_Validate тестирует валидацию пользователя.
func TestUser_Validate(t *testing.T) {
	tests := []struct {
		name        string
		user        *User
		expectedErr error
	}{
		{
			name: "валидные данные",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "test@example.com",
				Name:  "Иван Петров",
			},
			expectedErr: nil,
		},
		{
			name: "невалидный email - пустой",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "",
				Name:  "Иван Петров",
			},
			expectedErr: ErrInvalidEmail,
		},
		{
			name: "невалидный email - только пробелы",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "   ",
				Name:  "Иван Петров",
			},
			expectedErr: ErrInvalidEmail,
		},
		{
			name: "невалидный email - без @",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "test.example.com",
				Name:  "Иван Петров",
			},
			expectedErr: ErrInvalidEmail,
		},
		{
			name: "невалидный email - без домена",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "test@",
				Name:  "Иван Петров",
			},
			expectedErr: ErrInvalidEmail,
		},
		{
			name: "пустое имя",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "test@example.com",
				Name:  "",
			},
			expectedErr: ErrEmptyName,
		},
		{
			name: "имя только из пробелов",
			user: &User{
				ID:    "123e4567-e89b-12d3-a456-426614174000",
				Email: "test@example.com",
				Name:  "   ",
			},
			expectedErr: ErrEmptyName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.user.Validate()
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestUser_ValidateEmail тестирует валидацию email.
func TestUser_ValidateEmail(t *testing.T) {
	tests := []struct {
		name        string
		email       string
		expectedErr error
	}{
		{
			name:        "валидный email",
			email:       "user@example.com",
			expectedErr: nil,
		},
		{
			name:        "валидный email с поддоменом",
			email:       "user@mail.example.com",
			expectedErr: nil,
		},
		{
			name:        "валидный email с точкой в имени",
			email:       "user.name@example.com",
			expectedErr: nil,
		},
		{
			name:        "валидный email с плюсом",
			email:       "user+tag@example.com",
			expectedErr: nil,
		},
		{
			name:        "невалидный - пустой",
			email:       "",
			expectedErr: ErrInvalidEmail,
		},
		{
			name:        "невалидный - без @",
			email:       "userexample.com",
			expectedErr: ErrInvalidEmail,
		},
		{
			name:        "невалидный - несколько @",
			email:       "user@@example.com",
			expectedErr: ErrInvalidEmail,
		},
		{
			name:        "невалидный - без TLD",
			email:       "user@example",
			expectedErr: ErrInvalidEmail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := &User{Email: tt.email}
			err := user.ValidateEmail()
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidatePassword тестирует валидацию пароля.
func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name        string
		password    string
		expectedErr error
	}{
		{
			name:        "валидный пароль - 8 символов",
			password:    "12345678",
			expectedErr: nil,
		},
		{
			name:        "валидный пароль - длинный",
			password:    "очень_сложный_пароль_123!",
			expectedErr: nil,
		},
		{
			name:        "слабый пароль - 7 символов",
			password:    "1234567",
			expectedErr: ErrWeakPassword,
		},
		{
			name:        "слабый пароль - пустой",
			password:    "",
			expectedErr: ErrWeakPassword,
		},
		{
			name:        "слабый пароль - 1 символ",
			password:    "a",
			expectedErr: ErrWeakPassword,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
