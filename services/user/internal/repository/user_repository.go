// Package repository содержит реализацию доступа к данным для User Service.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"example.com/order-system/services/user/internal/domain"
)

// UserRepository определяет интерфейс для работы с пользователями в БД.
type UserRepository interface {
	// Create создаёт нового пользователя.
	Create(ctx context.Context, user *domain.User) error

	// GetByID возвращает пользователя по ID.
	GetByID(ctx context.Context, id string) (*domain.User, error)

	// GetByEmail возвращает пользователя по email.
	GetByEmail(ctx context.Context, email string) (*domain.User, error)

	// ExistsByEmail проверяет, существует ли пользователь с таким email.
	ExistsByEmail(ctx context.Context, email string) (bool, error)
}

// UserModel — GORM модель для таблицы users.
// Отделена от доменной сущности для гибкости.
type UserModel struct {
	ID        string    `gorm:"column:id;type:varchar(36);primaryKey"`
	Name      string    `gorm:"column:name;type:varchar(100);not null"`
	Email     string    `gorm:"column:email;type:varchar(255);uniqueIndex;not null"`
	Password  string    `gorm:"column:password;type:varchar(255);not null"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName возвращает имя таблицы в БД.
func (UserModel) TableName() string {
	return "users"
}

// toDomain конвертирует GORM модель в доменную сущность.
func (m *UserModel) toDomain() *domain.User {
	return &domain.User{
		ID:        m.ID,
		Name:      m.Name,
		Email:     m.Email,
		Password:  m.Password,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// fromDomain конвертирует доменную сущность в GORM модель.
func fromDomain(u *domain.User) *UserModel {
	return &UserModel{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Password:  u.Password,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// userRepository — GORM реализация UserRepository.
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository создаёт новый репозиторий пользователей.
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// Create создаёт нового пользователя в БД.
func (r *userRepository) Create(ctx context.Context, user *domain.User) error {
	model := fromDomain(user)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		// Проверяем на дубликат email (MySQL error 1062)
		if isDuplicateKeyError(err) {
			return domain.ErrEmailExists
		}
		return err
	}

	// Обновляем timestamps в доменной сущности
	user.CreatedAt = model.CreatedAt
	user.UpdatedAt = model.UpdatedAt

	return nil
}

// GetByID возвращает пользователя по ID.
func (r *userRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	var model UserModel

	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrUserNotFound
		}
		return nil, err
	}

	return model.toDomain(), nil
}

// GetByEmail возвращает пользователя по email.
func (r *userRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var model UserModel

	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrUserNotFound
		}
		return nil, err
	}

	return model.toDomain(), nil
}

// ExistsByEmail проверяет существование пользователя с заданным email.
func (r *userRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var count int64

	if err := r.db.WithContext(ctx).Model(&UserModel{}).Where("email = ?", email).Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

// isDuplicateKeyError проверяет, является ли ошибка дубликатом ключа.
// MySQL возвращает ошибку с кодом 1062 при попытке вставить дубликат.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// GORM v2 имеет ErrDuplicatedKey, также проверяем текст ошибки MySQL
	errMsg := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errMsg, "Duplicate entry") ||
		strings.Contains(errMsg, "1062")
}
