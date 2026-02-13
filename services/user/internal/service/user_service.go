// Package service содержит бизнес-логику User Service.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"example.com/order-system/pkg/jwt"
	"example.com/order-system/pkg/logger"
	"example.com/order-system/services/user/internal/domain"
	"example.com/order-system/services/user/internal/repository"
)

// bcryptCost — стоимость хэширования bcrypt.
// Значение 12 обеспечивает хороший баланс безопасности и производительности.
const bcryptCost = 12

// Blacklist определяет интерфейс для работы с blacklist токенов.
// Позволяет мокать jwt.Blacklist в тестах.
type Blacklist interface {
	Add(ctx context.Context, jti string, expiresAt time.Time) error
	Check(ctx context.Context, jti string) (bool, error)
}

// JWTManager определяет интерфейс для работы с JWT токенами.
// Позволяет мокать jwt.Manager в тестах.
type JWTManager interface {
	GenerateTokenPair(userID, role string) (*jwt.TokenPair, error)
	ValidateToken(tokenString string) (*jwt.Claims, error)
	ValidateWithBlacklist(ctx context.Context, tokenString string) (*jwt.Claims, error)
	Blacklist() Blacklist
}

// UserService определяет интерфейс бизнес-логики пользователей.
type UserService interface {
	// Register регистрирует нового пользователя.
	Register(ctx context.Context, email, password, name string) (userID string, err error)

	// Login аутентифицирует пользователя и возвращает токены.
	Login(ctx context.Context, email, password string) (*domain.TokenPair, error)

	// Logout инвалидирует токен (добавляет в blacklist).
	Logout(ctx context.Context, accessToken string) error

	// ValidateToken проверяет токен и возвращает claims.
	ValidateToken(ctx context.Context, accessToken string) (*domain.TokenClaims, error)

	// GetUser возвращает пользователя по ID.
	GetUser(ctx context.Context, userID string) (*domain.User, error)
}

// userService — реализация UserService.
type userService struct {
	repo         repository.UserRepository
	jwtManager   JWTManager
	loginLimiter LoginLimiter // nil = без ограничений (для тестов без Redis)
}

// NewUserService создаёт новый сервис пользователей.
// loginLimiter может быть nil — тогда защита от brute-force отключена.
func NewUserService(repo repository.UserRepository, jwtManager JWTManager, loginLimiter LoginLimiter) UserService {
	return &userService{
		repo:         repo,
		jwtManager:   jwtManager,
		loginLimiter: loginLimiter,
	}
}

// Register регистрирует нового пользователя.
func (s *userService) Register(ctx context.Context, email, password, name string) (string, error) {
	log := logger.FromContext(ctx)

	// Валидация пароля
	if err := domain.ValidatePassword(password); err != nil {
		log.Warn().Str("email", email).Msg("Попытка регистрации со слабым паролем")
		return "", err
	}

	// Создаём доменную сущность для валидации
	user := &domain.User{
		ID:    uuid.New().String(),
		Email: email,
		Name:  name,
	}

	// Валидация email и name
	if err := user.Validate(); err != nil {
		log.Warn().Str("email", email).Err(err).Msg("Ошибка валидации данных пользователя")
		return "", err
	}

	// Проверяем, не занят ли email
	exists, err := s.repo.ExistsByEmail(ctx, email)
	if err != nil {
		log.Error().Err(err).Str("email", email).Msg("Ошибка проверки существования email")
		return "", fmt.Errorf("ошибка проверки email: %w", err)
	}
	if exists {
		log.Warn().Str("email", email).Msg("Попытка регистрации с занятым email")
		return "", domain.ErrEmailExists
	}

	// Хэшируем пароль
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		log.Error().Err(err).Msg("Ошибка хэширования пароля")
		return "", fmt.Errorf("ошибка хэширования пароля: %w", err)
	}
	user.Password = string(hash)

	// Сохраняем пользователя
	if err := s.repo.Create(ctx, user); err != nil {
		log.Error().Err(err).Str("email", email).Msg("Ошибка создания пользователя")
		return "", fmt.Errorf("ошибка создания пользователя: %w", err)
	}

	log.Info().
		Str("user_id", user.ID).
		Str("email", email).
		Msg("Пользователь успешно зарегистрирован")

	return user.ID, nil
}

// Login аутентифицирует пользователя и возвращает токены.
// При включённом LoginLimiter: после 5 неудачных попыток блокирует аккаунт на 15 минут.
func (s *userService) Login(ctx context.Context, email, password string) (*domain.TokenPair, error) {
	log := logger.FromContext(ctx)

	// Проверяем блокировку аккаунта (если limiter настроен)
	if s.loginLimiter != nil {
		locked, err := s.loginLimiter.IsLocked(ctx, email)
		if err != nil {
			log.Error().Err(err).Str("email", email).Msg("Ошибка проверки блокировки аккаунта")
			// При ошибке Redis — пропускаем проверку, не блокируем пользователя
		} else if locked {
			log.Warn().Str("email", email).Msg("Попытка входа в заблокированный аккаунт")
			return nil, domain.ErrAccountLocked
		}
	}

	// Получаем пользователя по email
	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			log.Warn().Str("email", email).Msg("Попытка входа с несуществующим email")
			// Записываем неудачную попытку (защита от перебора email)
			s.recordLoginFailure(ctx, email)
			return nil, domain.ErrInvalidCredentials
		}
		log.Error().Err(err).Str("email", email).Msg("Ошибка получения пользователя")
		return nil, fmt.Errorf("ошибка получения пользователя: %w", err)
	}

	// Проверяем пароль
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		log.Warn().Str("email", email).Str("user_id", user.ID).Msg("Неверный пароль")
		s.recordLoginFailure(ctx, email)
		return nil, domain.ErrInvalidCredentials
	}

	// Успешный вход — сбрасываем счётчик попыток
	s.resetLoginAttempts(ctx, email)

	// Генерируем токены через pkg/jwt
	tokenPair, err := s.jwtManager.GenerateTokenPair(user.ID, "")
	if err != nil {
		log.Error().Err(err).Str("user_id", user.ID).Msg("Ошибка генерации токенов")
		return nil, fmt.Errorf("ошибка генерации токенов: %w", err)
	}

	log.Info().
		Str("user_id", user.ID).
		Str("email", email).
		Msg("Пользователь успешно вошёл в систему")

	return &domain.TokenPair{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
	}, nil
}

// recordLoginFailure записывает неудачную попытку входа (если limiter доступен).
func (s *userService) recordLoginFailure(ctx context.Context, email string) {
	if s.loginLimiter == nil {
		return
	}
	if err := s.loginLimiter.RecordFailure(ctx, email); err != nil {
		log := logger.FromContext(ctx)
		log.Error().Err(err).Str("email", email).Msg("Ошибка записи неудачной попытки входа")
	}
}

// resetLoginAttempts сбрасывает счётчик попыток после успешного входа.
func (s *userService) resetLoginAttempts(ctx context.Context, email string) {
	if s.loginLimiter == nil {
		return
	}
	if err := s.loginLimiter.ResetAttempts(ctx, email); err != nil {
		log := logger.FromContext(ctx)
		log.Error().Err(err).Str("email", email).Msg("Ошибка сброса счётчика попыток")
	}
}

// Logout инвалидирует токен.
func (s *userService) Logout(ctx context.Context, accessToken string) error {
	log := logger.FromContext(ctx)

	// Валидируем токен для получения claims
	claims, err := s.jwtManager.ValidateToken(accessToken)
	if err != nil {
		log.Warn().Err(err).Msg("Попытка logout с невалидным токеном")
		return domain.ErrInvalidToken
	}

	// Добавляем токен в blacklist
	blacklist := s.jwtManager.Blacklist()
	if blacklist == nil {
		log.Warn().Str("user_id", claims.UserID).Msg("Blacklist не настроен, токен не добавлен")
		return nil
	}

	if err := blacklist.Add(ctx, claims.ID, claims.ExpiresAt.Time); err != nil {
		log.Error().Err(err).Str("jti", claims.ID).Msg("Ошибка добавления токена в blacklist")
		return fmt.Errorf("ошибка отзыва токена: %w", err)
	}

	log.Info().
		Str("user_id", claims.UserID).
		Str("jti", claims.ID).
		Msg("Токен успешно отозван")

	return nil
}

// ValidateToken проверяет токен и возвращает claims.
func (s *userService) ValidateToken(ctx context.Context, accessToken string) (*domain.TokenClaims, error) {
	log := logger.FromContext(ctx)

	// Валидация с проверкой blacklist
	claims, err := s.jwtManager.ValidateWithBlacklist(ctx, accessToken)
	if err != nil {
		log.Debug().Err(err).Msg("Токен не прошёл валидацию")
		return nil, domain.ErrInvalidToken
	}

	// Получаем email пользователя из БД для полноты данных
	user, err := s.repo.GetByID(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			log.Warn().Str("user_id", claims.UserID).Msg("Токен валиден, но пользователь не найден")
			return nil, domain.ErrUserNotFound
		}
		log.Error().Err(err).Str("user_id", claims.UserID).Msg("Ошибка получения пользователя")
		return nil, fmt.Errorf("ошибка получения пользователя: %w", err)
	}

	return &domain.TokenClaims{
		UserID:    claims.UserID,
		Email:     user.Email,
		JTI:       claims.ID,
		IssuedAt:  claims.IssuedAt.Time,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// GetUser возвращает пользователя по ID.
func (s *userService) GetUser(ctx context.Context, userID string) (*domain.User, error) {
	log := logger.FromContext(ctx)

	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			log.Debug().Str("user_id", userID).Msg("Пользователь не найден")
			return nil, err
		}
		log.Error().Err(err).Str("user_id", userID).Msg("Ошибка получения пользователя")
		return nil, fmt.Errorf("ошибка получения пользователя: %w", err)
	}

	return user, nil
}
