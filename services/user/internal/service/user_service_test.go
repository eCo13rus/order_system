// Package service содержит unit тесты для UserService.
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	pkgjwt "example.com/order-system/pkg/jwt"
	"example.com/order-system/services/user/internal/domain"
)

// =====================================
// Моки
// =====================================

// MockUserRepository — мок для UserRepository.
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) Create(ctx context.Context, user *domain.User) error {
	return m.Called(ctx, user).Error(0)
}

func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	args := m.Called(ctx, email)
	return args.Bool(0), args.Error(1)
}

// MockBlacklist — мок для jwt.Blacklist.
type MockBlacklist struct {
	mock.Mock
}

func (m *MockBlacklist) Add(ctx context.Context, jti string, expiresAt time.Time) error {
	return m.Called(ctx, jti, expiresAt).Error(0)
}

func (m *MockBlacklist) Check(ctx context.Context, jti string) (bool, error) {
	args := m.Called(ctx, jti)
	return args.Bool(0), args.Error(1)
}

// MockJWTManager — мок для JWT операций.
type MockJWTManager struct {
	mock.Mock
	blacklist *MockBlacklist
}

func (m *MockJWTManager) GenerateTokenPair(userID, role string) (*pkgjwt.TokenPair, error) {
	args := m.Called(userID, role)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*pkgjwt.TokenPair), args.Error(1)
}

func (m *MockJWTManager) ValidateToken(tokenString string) (*pkgjwt.Claims, error) {
	args := m.Called(tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*pkgjwt.Claims), args.Error(1)
}

func (m *MockJWTManager) ValidateWithBlacklist(ctx context.Context, tokenString string) (*pkgjwt.Claims, error) {
	args := m.Called(ctx, tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*pkgjwt.Claims), args.Error(1)
}

func (m *MockJWTManager) Blacklist() Blacklist {
	// ВАЖНО: явная проверка на nil, иначе вернётся non-nil interface с nil pointer
	if m.blacklist == nil {
		return nil
	}
	return m.blacklist
}
func (m *MockJWTManager) SetBlacklist(bl *MockBlacklist) { m.blacklist = bl }

// =====================================
// Тесты Register
// =====================================

func TestRegister(t *testing.T) {
	tests := []struct {
		name        string
		email       string
		password    string
		userName    string
		mockSetup   func(*MockUserRepository)
		expectedErr error
	}{
		{
			name:     "успешная регистрация",
			email:    "new@example.com",
			password: "strongPass123",
			userName: "Новый Пользователь",
			mockSetup: func(m *MockUserRepository) {
				m.On("ExistsByEmail", mock.Anything, "new@example.com").Return(false, nil)
				m.On("Create", mock.Anything, mock.AnythingOfType("*domain.User")).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:        "слабый пароль",
			email:       "test@example.com",
			password:    "short",
			userName:    "Тест",
			mockSetup:   func(m *MockUserRepository) {},
			expectedErr: domain.ErrWeakPassword,
		},
		{
			name:        "невалидный email",
			email:       "not-an-email",
			password:    "strongPass123",
			userName:    "Тест",
			mockSetup:   func(m *MockUserRepository) {},
			expectedErr: domain.ErrInvalidEmail,
		},
		{
			name:        "пустое имя",
			email:       "test@example.com",
			password:    "strongPass123",
			userName:    "",
			mockSetup:   func(m *MockUserRepository) {},
			expectedErr: domain.ErrEmptyName,
		},
		{
			name:     "email уже занят",
			email:    "taken@example.com",
			password: "strongPass123",
			userName: "Дубликат",
			mockSetup: func(m *MockUserRepository) {
				m.On("ExistsByEmail", mock.Anything, "taken@example.com").Return(true, nil)
			},
			expectedErr: domain.ErrEmailExists,
		},
		{
			name:     "ошибка БД при проверке email",
			email:    "test@example.com",
			password: "strongPass123",
			userName: "Тест",
			mockSetup: func(m *MockUserRepository) {
				m.On("ExistsByEmail", mock.Anything, "test@example.com").Return(false, errors.New("db error"))
			},
			expectedErr: errors.New("db error"),
		},
		{
			name:     "ошибка БД при создании",
			email:    "test@example.com",
			password: "strongPass123",
			userName: "Тест",
			mockSetup: func(m *MockUserRepository) {
				m.On("ExistsByEmail", mock.Anything, "test@example.com").Return(false, nil)
				m.On("Create", mock.Anything, mock.AnythingOfType("*domain.User")).Return(errors.New("db error"))
			},
			expectedErr: errors.New("db error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockUserRepository)
			tt.mockSetup(mockRepo)

			svc := &userService{repo: mockRepo, jwtManager: nil}
			userID, err := svc.Register(context.Background(), tt.email, tt.password, tt.userName)

			if tt.expectedErr != nil {
				require.Error(t, err)
				// Для доменных ошибок проверяем ErrorIs
				if errors.Is(tt.expectedErr, domain.ErrWeakPassword) ||
					errors.Is(tt.expectedErr, domain.ErrInvalidEmail) ||
					errors.Is(tt.expectedErr, domain.ErrEmptyName) ||
					errors.Is(tt.expectedErr, domain.ErrEmailExists) {
					assert.ErrorIs(t, err, tt.expectedErr)
				} else {
					assert.Contains(t, err.Error(), tt.expectedErr.Error())
				}
				assert.Empty(t, userID)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, userID)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты Login
// =====================================

func TestLogin(t *testing.T) {
	correctPassword := "correctPass123"
	hash, _ := bcrypt.GenerateFromPassword([]byte(correctPassword), bcrypt.MinCost)

	validUser := &domain.User{
		ID:       "user-123",
		Name:     "Валидный Пользователь",
		Email:    "valid@example.com",
		Password: string(hash),
	}

	// Токены для успешного сценария
	expectedTokenPair := &pkgjwt.TokenPair{
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-123",
		ExpiresAt:    time.Now().Add(15 * time.Minute).Unix(),
	}

	tests := []struct {
		name        string
		email       string
		password    string
		mockSetup   func(*MockUserRepository, *MockJWTManager)
		expectedErr error
		checkResult func(t *testing.T, tokens *domain.TokenPair)
	}{
		{
			name:     "успешный логин",
			email:    "valid@example.com",
			password: correctPassword,
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				repo.On("GetByEmail", mock.Anything, "valid@example.com").Return(validUser, nil)
				jwt.On("GenerateTokenPair", "user-123", "").Return(expectedTokenPair, nil)
			},
			expectedErr: nil,
			checkResult: func(t *testing.T, tokens *domain.TokenPair) {
				assert.Equal(t, "access-token-123", tokens.AccessToken)
				assert.Equal(t, "refresh-token-123", tokens.RefreshToken)
			},
		},
		{
			name:     "пользователь не найден",
			email:    "unknown@example.com",
			password: "anyPassword123",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				repo.On("GetByEmail", mock.Anything, "unknown@example.com").Return(nil, domain.ErrUserNotFound)
			},
			expectedErr: domain.ErrInvalidCredentials,
		},
		{
			name:     "неверный пароль",
			email:    "valid@example.com",
			password: "wrongPassword123",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				repo.On("GetByEmail", mock.Anything, "valid@example.com").Return(validUser, nil)
			},
			expectedErr: domain.ErrInvalidCredentials,
		},
		{
			name:     "ошибка БД при получении пользователя",
			email:    "test@example.com",
			password: "anyPassword123",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				repo.On("GetByEmail", mock.Anything, "test@example.com").Return(nil, errors.New("db error"))
			},
			expectedErr: errors.New("db error"),
		},
		{
			name:     "ошибка генерации токенов",
			email:    "valid@example.com",
			password: correctPassword,
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				repo.On("GetByEmail", mock.Anything, "valid@example.com").Return(validUser, nil)
				jwt.On("GenerateTokenPair", "user-123", "").Return(nil, errors.New("jwt signing error"))
			},
			expectedErr: errors.New("jwt signing error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockUserRepository)
			mockJWT := new(MockJWTManager)
			tt.mockSetup(mockRepo, mockJWT)

			svc := NewUserService(mockRepo, mockJWT, nil)
			tokens, err := svc.Login(context.Background(), tt.email, tt.password)

			if tt.expectedErr != nil {
				require.Error(t, err)
				if errors.Is(tt.expectedErr, domain.ErrInvalidCredentials) {
					assert.ErrorIs(t, err, tt.expectedErr)
				} else {
					assert.Contains(t, err.Error(), tt.expectedErr.Error())
				}
				assert.Nil(t, tokens)
			} else {
				require.NoError(t, err)
				require.NotNil(t, tokens)
				if tt.checkResult != nil {
					tt.checkResult(t, tokens)
				}
			}
			mockRepo.AssertExpectations(t)
			mockJWT.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты Logout
// =====================================

func TestLogout(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	validClaims := &pkgjwt.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-123",
			Subject:   "user-123",
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		UserID: "user-123",
	}

	tests := []struct {
		name        string
		token       string
		mockSetup   func(*MockJWTManager, *MockBlacklist)
		expectedErr error
	}{
		{
			name:  "успешный logout",
			token: "valid-token",
			mockSetup: func(jwt *MockJWTManager, bl *MockBlacklist) {
				jwt.On("ValidateToken", "valid-token").Return(validClaims, nil)
				bl.On("Add", mock.Anything, "jti-123", expiresAt).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:  "невалидный токен",
			token: "invalid-token",
			mockSetup: func(jwt *MockJWTManager, bl *MockBlacklist) {
				jwt.On("ValidateToken", "invalid-token").Return(nil, errors.New("token expired"))
			},
			expectedErr: domain.ErrInvalidToken,
		},
		{
			name:  "ошибка blacklist",
			token: "valid-token",
			mockSetup: func(jwt *MockJWTManager, bl *MockBlacklist) {
				jwt.On("ValidateToken", "valid-token").Return(validClaims, nil)
				bl.On("Add", mock.Anything, "jti-123", expiresAt).Return(errors.New("redis error"))
			},
			expectedErr: errors.New("redis error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockUserRepository)
			mockJWT := new(MockJWTManager)
			mockBlacklist := new(MockBlacklist)
			mockJWT.SetBlacklist(mockBlacklist)
			tt.mockSetup(mockJWT, mockBlacklist)

			svc := NewUserService(mockRepo, mockJWT, nil)
			err := svc.Logout(context.Background(), tt.token)

			if tt.expectedErr != nil {
				require.Error(t, err)
				if errors.Is(tt.expectedErr, domain.ErrInvalidToken) {
					assert.ErrorIs(t, err, domain.ErrInvalidToken)
				} else {
					assert.Contains(t, err.Error(), tt.expectedErr.Error())
				}
			} else {
				require.NoError(t, err)
			}
			mockJWT.AssertExpectations(t)
			mockBlacklist.AssertExpectations(t)
		})
	}
}

// TestLogoutWithNilBlacklist проверяет logout когда blacklist не настроен.
// Это graceful degradation — система продолжает работать без blacklist.
func TestLogoutWithNilBlacklist(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	validClaims := &pkgjwt.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-456",
			Subject:   "user-456",
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		UserID: "user-456",
	}

	mockRepo := new(MockUserRepository)
	mockJWT := new(MockJWTManager)
	// НЕ вызываем SetBlacklist — blacklist остаётся nil
	mockJWT.On("ValidateToken", "valid-token").Return(validClaims, nil)

	svc := NewUserService(mockRepo, mockJWT, nil)
	err := svc.Logout(context.Background(), "valid-token")

	// Ожидаем успех — logout без blacklist не должен падать
	require.NoError(t, err)
	mockJWT.AssertExpectations(t)
}

// =====================================
// Тесты ValidateToken
// =====================================

func TestValidateToken(t *testing.T) {
	now := time.Now()
	validClaims := &pkgjwt.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-789",
			Subject:   "user-789",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
		UserID: "user-789",
	}
	validUser := &domain.User{ID: "user-789", Email: "test@example.com", Name: "Test User"}

	tests := []struct {
		name        string
		token       string
		mockSetup   func(*MockUserRepository, *MockJWTManager)
		expectedErr error
		checkResult func(t *testing.T, result *domain.TokenClaims)
	}{
		{
			name:  "успешная валидация",
			token: "valid-token",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				jwt.On("ValidateWithBlacklist", mock.Anything, "valid-token").Return(validClaims, nil)
				repo.On("GetByID", mock.Anything, "user-789").Return(validUser, nil)
			},
			expectedErr: nil,
			checkResult: func(t *testing.T, result *domain.TokenClaims) {
				assert.Equal(t, "user-789", result.UserID)
				assert.Equal(t, "test@example.com", result.Email)
				assert.Equal(t, "jti-789", result.JTI)
			},
		},
		{
			name:  "невалидный токен",
			token: "invalid-token",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				jwt.On("ValidateWithBlacklist", mock.Anything, "invalid-token").Return(nil, errors.New("token expired"))
			},
			expectedErr: domain.ErrInvalidToken,
			checkResult: nil,
		},
		{
			name:  "пользователь не найден",
			token: "valid-token",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				jwt.On("ValidateWithBlacklist", mock.Anything, "valid-token").Return(validClaims, nil)
				repo.On("GetByID", mock.Anything, "user-789").Return(nil, domain.ErrUserNotFound)
			},
			expectedErr: domain.ErrUserNotFound,
			checkResult: nil,
		},
		{
			name:  "ошибка БД при получении пользователя",
			token: "valid-token",
			mockSetup: func(repo *MockUserRepository, jwt *MockJWTManager) {
				jwt.On("ValidateWithBlacklist", mock.Anything, "valid-token").Return(validClaims, nil)
				repo.On("GetByID", mock.Anything, "user-789").Return(nil, errors.New("database connection lost"))
			},
			expectedErr: errors.New("database connection lost"),
			checkResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockUserRepository)
			mockJWT := new(MockJWTManager)
			tt.mockSetup(mockRepo, mockJWT)

			svc := NewUserService(mockRepo, mockJWT, nil)
			result, err := svc.ValidateToken(context.Background(), tt.token)

			if tt.expectedErr != nil {
				require.Error(t, err)
				// Для доменных ошибок используем ErrorIs, для остальных — Contains
				if errors.Is(tt.expectedErr, domain.ErrInvalidToken) ||
					errors.Is(tt.expectedErr, domain.ErrUserNotFound) {
					assert.ErrorIs(t, err, tt.expectedErr)
				} else {
					assert.Contains(t, err.Error(), tt.expectedErr.Error())
				}
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if tt.checkResult != nil {
					tt.checkResult(t, result)
				}
			}
			mockRepo.AssertExpectations(t)
			mockJWT.AssertExpectations(t)
		})
	}
}

// =====================================
// Тесты GetUser
// =====================================

func TestGetUser(t *testing.T) {
	expectedUser := &domain.User{ID: "user-123", Email: "test@example.com", Name: "Тест"}

	tests := []struct {
		name        string
		userID      string
		mockSetup   func(*MockUserRepository)
		expectedErr error
		checkResult func(t *testing.T, user *domain.User)
	}{
		{
			name:   "успешное получение",
			userID: "user-123",
			mockSetup: func(m *MockUserRepository) {
				m.On("GetByID", mock.Anything, "user-123").Return(expectedUser, nil)
			},
			expectedErr: nil,
			checkResult: func(t *testing.T, user *domain.User) {
				assert.Equal(t, expectedUser.ID, user.ID)
				assert.Equal(t, expectedUser.Email, user.Email)
			},
		},
		{
			name:   "пользователь не найден",
			userID: "unknown-user",
			mockSetup: func(m *MockUserRepository) {
				m.On("GetByID", mock.Anything, "unknown-user").Return(nil, domain.ErrUserNotFound)
			},
			expectedErr: domain.ErrUserNotFound,
			checkResult: nil,
		},
		{
			name:   "ошибка БД при получении",
			userID: "user-456",
			mockSetup: func(m *MockUserRepository) {
				m.On("GetByID", mock.Anything, "user-456").Return(nil, errors.New("connection refused"))
			},
			expectedErr: errors.New("connection refused"),
			checkResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockUserRepository)
			tt.mockSetup(mockRepo)

			svc := &userService{repo: mockRepo, jwtManager: nil}
			user, err := svc.GetUser(context.Background(), tt.userID)

			if tt.expectedErr != nil {
				require.Error(t, err)
				// Для доменных ошибок используем ErrorIs, для остальных — Contains
				if errors.Is(tt.expectedErr, domain.ErrUserNotFound) {
					assert.ErrorIs(t, err, tt.expectedErr)
				} else {
					assert.Contains(t, err.Error(), tt.expectedErr.Error())
				}
				assert.Nil(t, user)
			} else {
				require.NoError(t, err)
				require.NotNil(t, user)
				if tt.checkResult != nil {
					tt.checkResult(t, user)
				}
			}
			mockRepo.AssertExpectations(t)
		})
	}
}
