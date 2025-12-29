// Package service содержит адаптер для jwt.Manager.
package service

import (
	"context"

	"example.com/order-system/pkg/jwt"
)

// jwtManagerAdapter оборачивает *jwt.Manager для реализации JWTManager интерфейса.
// Необходим потому что *jwt.Manager.Blacklist() возвращает *jwt.Blacklist,
// а интерфейс требует service.Blacklist.
type jwtManagerAdapter struct {
	manager *jwt.Manager
}

// NewJWTManagerAdapter создаёт адаптер для jwt.Manager.
func NewJWTManagerAdapter(manager *jwt.Manager) JWTManager {
	return &jwtManagerAdapter{manager: manager}
}

func (a *jwtManagerAdapter) GenerateTokenPair(userID, role string) (*jwt.TokenPair, error) {
	return a.manager.GenerateTokenPair(userID, role)
}

func (a *jwtManagerAdapter) ValidateToken(tokenString string) (*jwt.Claims, error) {
	return a.manager.ValidateToken(tokenString)
}

func (a *jwtManagerAdapter) ValidateWithBlacklist(ctx context.Context, tokenString string) (*jwt.Claims, error) {
	return a.manager.ValidateWithBlacklist(ctx, tokenString)
}

func (a *jwtManagerAdapter) Blacklist() Blacklist {
	// *jwt.Blacklist реализует Blacklist интерфейс (имеет Add и Check)
	return a.manager.Blacklist()
}
