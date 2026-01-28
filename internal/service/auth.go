package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	intjwtauth "github.com/go-chi/jwtauth/v5"
	models "github.com/idudko/go-musthave-diploma/internal/model"
	"github.com/idudko/go-musthave-diploma/internal/repository"
)

type AuthService struct {
	repo      *repository.Repository
	jwtAuth   *intjwtauth.JWTAuth
	jwtSecret string
}

func NewAuthService(repo *repository.Repository) *AuthService {
	// Используем секретный ключ для JWT
	jwtSecret := "gophermart-secret-key"
	jwtAuth := intjwtauth.New("HS256", []byte(jwtSecret), nil)

	return &AuthService{
		repo:      repo,
		jwtAuth:   jwtAuth,
		jwtSecret: jwtSecret,
	}
}

func (s *AuthService) HashPassword(password string, salt string) string {
	h := hmac.New(sha256.New, []byte(salt))
	h.Write([]byte(password))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *AuthService) RegisterUser(ctx context.Context, login, password string) (*models.User, string, error) {
	// Проверяем, не занят ли логин
	existingUser, err := s.repo.GetUserByLogin(ctx, login)
	if err != nil {
		return nil, "", fmt.Errorf("failed to check user existence: %w", err)
	}
	if existingUser != nil {
		return nil, "", fmt.Errorf("login already taken")
	}

	// Хешируем пароль
	salt := login // Используем логин как соль
	hashedPassword := s.HashPassword(password, salt)

	// Создаем пользователя
	userID, err := s.repo.CreateUser(ctx, login, hashedPassword)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create user: %w", err)
	}

	// Получаем созданного пользователя
	user, err := s.repo.GetUserByLogin(ctx, login)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get created user: %w", err)
	}

	// Создаем JWT токен
	token, err := s.GenerateToken(userID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}

	return user, token, nil
}

func (s *AuthService) LoginUser(ctx context.Context, login, password string) (*models.User, string, error) {
	// Получаем пользователя
	user, err := s.repo.GetUserByLogin(ctx, login)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, "", fmt.Errorf("user not found")
	}

	// Проверяем пароль
	salt := login // Используем логин как соль
	hashedPassword := s.HashPassword(password, salt)
	if user.Password != hashedPassword {
		return nil, "", fmt.Errorf("invalid password")
	}

	// Создаем JWT токен
	token, err := s.GenerateToken(user.ID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}

	return user, token, nil
}

func (s *AuthService) GenerateToken(userID int64) (string, error) {
	// Создаем JWT токен с указанием ID пользователя в claims
	_, tokenString, err := s.jwtAuth.Encode(map[string]interface{}{
		"user_id": userID,
		"exp":     time.Now().Add(time.Hour * 24 * 30).Unix(), // Токен действителен 30 дней
	})

	if err != nil {
		return "", err
	}

	return tokenString, nil
}

// ValidateToken проверяет валидность токена и возвращает ID пользователя
func (s *AuthService) ValidateToken(tokenString string) (int64, bool) {
	token, err := s.jwtAuth.Decode(tokenString)
	if err != nil {
		return 0, false
	}

	if token == nil {
		return 0, false
	}

	claims, err := token.AsMap(context.Background())
	if err != nil {
		return 0, false
	}

	userID, ok := claims["user_id"].(float64)
	if !ok {
		return 0, false
	}

	return int64(userID), true
}

// GetJWTAuth возвращает экземпляр jwtauth.JWTAuth для использования в middleware
func (s *AuthService) GetJWTAuth() *intjwtauth.JWTAuth {
	return s.jwtAuth
}
