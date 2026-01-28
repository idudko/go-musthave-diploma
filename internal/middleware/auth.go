package middleware

import (
	"net/http"

	"github.com/go-chi/jwtauth/v5"
	"github.com/rs/zerolog"
)

// AuthMiddleware представляет middleware для аутентификации пользователя
type AuthMiddleware struct {
	jwtAuth *jwtauth.JWTAuth
	logger  *zerolog.Logger
}

// NewAuthMiddleware создает новый экземпляр AuthMiddleware
func NewAuthMiddleware(jwtAuth *jwtauth.JWTAuth, logger *zerolog.Logger) *AuthMiddleware {
	return &AuthMiddleware{
		jwtAuth: jwtAuth,
		logger:  logger,
	}
}

// Middleware возвращает middleware-обработчик для проверки аутентификации пользователя
func (m *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return jwtauth.Verifier(m.jwtAuth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, claims, err := jwtauth.FromContext(r.Context())

		// Если токен отсутствует или невалидный
		if err != nil {
			m.logger.Error().Err(err).Msg("Authentication failed")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Проверяем наличие user_id в claims
		if claims == nil || claims["user_id"] == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Передаем управление следующему обработчику
		next.ServeHTTP(w, r)
	}))
}

// GetUserIDFromContext получает ID пользователя из контекста запроса
func GetUserIDFromContext(r *http.Request) (int64, bool) {
	_, claims, err := jwtauth.FromContext(r.Context())
	if err != nil || claims == nil {
		return 0, false
	}

	userID, ok := claims["user_id"].(float64)
	if !ok {
		return 0, false
	}

	return int64(userID), true
}
