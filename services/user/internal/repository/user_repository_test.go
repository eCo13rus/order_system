// Package repository содержит unit тесты для UserRepository.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"example.com/order-system/services/user/internal/domain"
)

// =====================================
// Вспомогательные функции
// =====================================

// setupMockDB создаёт мок базы данных с GORM.
func setupMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err, "Ошибка создания sqlmock")

	dialector := mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	})

	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Ошибка инициализации GORM")

	return gormDB, mock, func() { _ = db.Close() }
}

// =====================================
// Тесты Create
// =====================================

func TestCreate(t *testing.T) {
	tests := []struct {
		name        string
		user        *domain.User
		mockSetup   func(mock sqlmock.Sqlmock, user *domain.User)
		expectedErr error
	}{
		{
			name: "успешное создание",
			user: &domain.User{
				ID:       "new-user-uuid",
				Name:     "Новый Пользователь",
				Email:    "new@example.com",
				Password: "hashed-password",
			},
			mockSetup: func(mock sqlmock.Sqlmock, user *domain.User) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `users`")).
					WithArgs(user.ID, user.Name, user.Email, user.Password, sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			},
			expectedErr: nil,
		},
		{
			name: "дубликат email",
			user: &domain.User{
				ID:       "dup-user-uuid",
				Name:     "Дубликат",
				Email:    "existing@example.com",
				Password: "hashed-password",
			},
			mockSetup: func(mock sqlmock.Sqlmock, user *domain.User) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `users`")).
					WithArgs(user.ID, user.Name, user.Email, user.Password, sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnError(errors.New("Error 1062: Duplicate entry"))
				mock.ExpectRollback()
			},
			expectedErr: domain.ErrEmailExists,
		},
		{
			name: "ошибка БД",
			user: &domain.User{
				ID:       "error-user-uuid",
				Name:     "Ошибка",
				Email:    "error@example.com",
				Password: "hashed-password",
			},
			mockSetup: func(mock sqlmock.Sqlmock, user *domain.User) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `users`")).
					WithArgs(user.ID, user.Name, user.Email, user.Password, sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnError(sql.ErrConnDone)
				mock.ExpectRollback()
			},
			expectedErr: sql.ErrConnDone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gormDB, mock, cleanup := setupMockDB(t)
			defer cleanup()

			repo := NewUserRepository(gormDB)
			tt.mockSetup(mock, tt.user)

			err := repo.Create(context.Background(), tt.user)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// =====================================
// Тесты GetByID
// =====================================

func TestGetByID(t *testing.T) {
	tests := []struct {
		name        string
		userID      string
		mockSetup   func(mock sqlmock.Sqlmock, userID string)
		expectedErr error
		checkUser   func(t *testing.T, user *domain.User)
	}{
		{
			name:   "успешное получение",
			userID: "user-123",
			mockSetup: func(mock sqlmock.Sqlmock, userID string) {
				now := time.Now().Truncate(time.Second)
				rows := sqlmock.NewRows([]string{"id", "name", "email", "password", "created_at", "updated_at"}).
					AddRow(userID, "Тест", "test@example.com", "hash", now, now)
				mock.ExpectQuery("SELECT \\* FROM `users` WHERE id = \\? ORDER BY `users`.`id` LIMIT \\?").
					WithArgs(userID, 1).WillReturnRows(rows)
			},
			expectedErr: nil,
			checkUser: func(t *testing.T, user *domain.User) {
				assert.Equal(t, "user-123", user.ID)
				assert.Equal(t, "test@example.com", user.Email)
			},
		},
		{
			name:   "не найден",
			userID: "unknown-user",
			mockSetup: func(mock sqlmock.Sqlmock, userID string) {
				rows := sqlmock.NewRows([]string{"id", "name", "email", "password", "created_at", "updated_at"})
				mock.ExpectQuery("SELECT \\* FROM `users` WHERE id = \\? ORDER BY `users`.`id` LIMIT \\?").
					WithArgs(userID, 1).WillReturnRows(rows)
			},
			expectedErr: domain.ErrUserNotFound,
			checkUser:   nil,
		},
		{
			name:   "ошибка БД",
			userID: "user-456",
			mockSetup: func(mock sqlmock.Sqlmock, userID string) {
				mock.ExpectQuery("SELECT \\* FROM `users` WHERE id = \\? ORDER BY `users`.`id` LIMIT \\?").
					WithArgs(userID, 1).WillReturnError(sql.ErrConnDone)
			},
			expectedErr: sql.ErrConnDone,
			checkUser:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gormDB, mock, cleanup := setupMockDB(t)
			defer cleanup()

			repo := NewUserRepository(gormDB)
			tt.mockSetup(mock, tt.userID)

			user, err := repo.GetByID(context.Background(), tt.userID)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Nil(t, user)
			} else {
				require.NoError(t, err)
				require.NotNil(t, user)
				if tt.checkUser != nil {
					tt.checkUser(t, user)
				}
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// =====================================
// Тесты GetByEmail
// =====================================

func TestGetByEmail(t *testing.T) {
	tests := []struct {
		name        string
		email       string
		mockSetup   func(mock sqlmock.Sqlmock, email string)
		expectedErr error
		checkUser   func(t *testing.T, user *domain.User)
	}{
		{
			name:  "успешное получение",
			email: "valid@example.com",
			mockSetup: func(mock sqlmock.Sqlmock, email string) {
				now := time.Now().Truncate(time.Second)
				rows := sqlmock.NewRows([]string{"id", "name", "email", "password", "created_at", "updated_at"}).
					AddRow("user-found", "Найденный", email, "hash123", now, now)
				mock.ExpectQuery("SELECT \\* FROM `users` WHERE email = \\? ORDER BY `users`.`id` LIMIT \\?").
					WithArgs(email, 1).WillReturnRows(rows)
			},
			expectedErr: nil,
			checkUser: func(t *testing.T, user *domain.User) {
				assert.Equal(t, "user-found", user.ID)
				assert.Equal(t, "valid@example.com", user.Email)
			},
		},
		{
			name:  "не найден",
			email: "notfound@example.com",
			mockSetup: func(mock sqlmock.Sqlmock, email string) {
				rows := sqlmock.NewRows([]string{"id", "name", "email", "password", "created_at", "updated_at"})
				mock.ExpectQuery("SELECT \\* FROM `users` WHERE email = \\? ORDER BY `users`.`id` LIMIT \\?").
					WithArgs(email, 1).WillReturnRows(rows)
			},
			expectedErr: domain.ErrUserNotFound,
			checkUser:   nil,
		},
		{
			name:  "ошибка БД",
			email: "error@example.com",
			mockSetup: func(mock sqlmock.Sqlmock, email string) {
				mock.ExpectQuery("SELECT \\* FROM `users` WHERE email = \\? ORDER BY `users`.`id` LIMIT \\?").
					WithArgs(email, 1).WillReturnError(sql.ErrConnDone)
			},
			expectedErr: sql.ErrConnDone,
			checkUser:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gormDB, mock, cleanup := setupMockDB(t)
			defer cleanup()

			repo := NewUserRepository(gormDB)
			tt.mockSetup(mock, tt.email)

			user, err := repo.GetByEmail(context.Background(), tt.email)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Nil(t, user)
			} else {
				require.NoError(t, err)
				require.NotNil(t, user)
				if tt.checkUser != nil {
					tt.checkUser(t, user)
				}
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// =====================================
// Тесты ExistsByEmail
// =====================================

func TestExistsByEmail(t *testing.T) {
	tests := []struct {
		name           string
		email          string
		mockSetup      func(mock sqlmock.Sqlmock, email string)
		expectedExists bool
		expectedErr    error
	}{
		{
			name:  "существует",
			email: "exists@example.com",
			mockSetup: func(mock sqlmock.Sqlmock, email string) {
				rows := sqlmock.NewRows([]string{"count"}).AddRow(1)
				mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `users` WHERE email = ?")).
					WithArgs(email).WillReturnRows(rows)
			},
			expectedExists: true,
			expectedErr:    nil,
		},
		{
			name:  "не существует",
			email: "new@example.com",
			mockSetup: func(mock sqlmock.Sqlmock, email string) {
				rows := sqlmock.NewRows([]string{"count"}).AddRow(0)
				mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `users` WHERE email = ?")).
					WithArgs(email).WillReturnRows(rows)
			},
			expectedExists: false,
			expectedErr:    nil,
		},
		{
			name:  "ошибка БД",
			email: "error@example.com",
			mockSetup: func(mock sqlmock.Sqlmock, email string) {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `users` WHERE email = ?")).
					WithArgs(email).WillReturnError(sql.ErrConnDone)
			},
			expectedExists: false,
			expectedErr:    sql.ErrConnDone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gormDB, mock, cleanup := setupMockDB(t)
			defer cleanup()

			repo := NewUserRepository(gormDB)
			tt.mockSetup(mock, tt.email)

			exists, err := repo.ExistsByEmail(context.Background(), tt.email)

			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectedExists, exists)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// =====================================
// Тесты конвертации Domain <-> Model
// =====================================

func TestUserModel_ToDomain(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	model := &UserModel{
		ID:        "model-uuid",
		Name:      "Модель",
		Email:     "model@example.com",
		Password:  "bcrypt-hash",
		CreatedAt: now,
		UpdatedAt: now,
	}

	user := model.toDomain()

	assert.Equal(t, model.ID, user.ID)
	assert.Equal(t, model.Name, user.Name)
	assert.Equal(t, model.Email, user.Email)
	assert.Equal(t, model.Password, user.Password)
}

func TestFromDomain(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	user := &domain.User{
		ID:        "domain-uuid",
		Name:      "Доменный",
		Email:     "domain@example.com",
		Password:  "domain-hash",
		CreatedAt: now,
		UpdatedAt: now,
	}

	model := fromDomain(user)

	assert.Equal(t, user.ID, model.ID)
	assert.Equal(t, user.Name, model.Name)
	assert.Equal(t, user.Email, model.Email)
	assert.Equal(t, user.Password, model.Password)
}

func TestUserModel_TableName(t *testing.T) {
	assert.Equal(t, "users", UserModel{}.TableName())
}

// =====================================
// Тесты isDuplicateKeyError
// =====================================

func TestIsDuplicateKeyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil ошибка", nil, false},
		{"MySQL Error 1062", errors.New("Error 1062: Duplicate entry"), true},
		{"Duplicate entry в тексте", errors.New("Duplicate entry 'email'"), true},
		{"GORM ErrDuplicatedKey", gorm.ErrDuplicatedKey, true},
		{"обычная ошибка", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isDuplicateKeyError(tt.err))
		})
	}
}
