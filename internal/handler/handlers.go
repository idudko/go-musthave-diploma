package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/idudko/go-musthave-diploma/internal/middleware"
	intmodel "github.com/idudko/go-musthave-diploma/internal/model"
	intservice "github.com/idudko/go-musthave-diploma/internal/service"
	"github.com/rs/zerolog"
)

type Handler struct {
	authSvc    *intservice.AuthService
	orderSvc   *intservice.OrderService
	accrualSvc *intservice.AccrualService
	logger     *zerolog.Logger
}

func NewHandler(authSvc *intservice.AuthService, orderSvc *intservice.OrderService, accrualSvc *intservice.AccrualService, logger *zerolog.Logger) *Handler {
	return &Handler{
		authSvc:    authSvc,
		orderSvc:   orderSvc,
		accrualSvc: accrualSvc,
		logger:     logger,
	}
}

func (h *Handler) RegisterUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if req.Login == "" || req.Password == "" {
		http.Error(w, "Login and password are required", http.StatusBadRequest)
		return
	}

	user, token, err := h.authSvc.RegisterUser(r.Context(), req.Login, req.Password)
	if err != nil {
		if err.Error() == "login already taken" {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.logger.Error().Err(err).Msg("Failed to register user")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Устанавливаем куку с токеном
	http.SetCookie(w, &http.Cookie{
		Name:  "jwt",
		Value: token,
		Path:  "/",
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":    user.ID,
		"login": user.Login,
	})
}

func (h *Handler) LoginUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if req.Login == "" || req.Password == "" {
		http.Error(w, "Login and password are required", http.StatusBadRequest)
		return
	}

	user, token, err := h.authSvc.LoginUser(r.Context(), req.Login, req.Password)
	if err != nil {
		if err.Error() == "user not found" || err.Error() == "invalid password" {
			http.Error(w, "Invalid login or password", http.StatusUnauthorized)
			return
		}
		h.logger.Error().Err(err).Msg("Failed to login user")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Устанавливаем куку с токеном
	http.SetCookie(w, &http.Cookie{
		Name:  "jwt",
		Value: token,
		Path:  "/",
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":    user.ID,
		"login": user.Login,
	})
}

func (h *Handler) UploadOrderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем аутентификацию пользователя
	userID, ok := middleware.GetUserIDFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Читаем номер заказа из тела запроса
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	orderNumber := string(body)
	if orderNumber == "" {
		http.Error(w, "Order number is required", http.StatusBadRequest)
		return
	}

	// Проверяем номер заказа по алгоритму Луна
	if !intmodel.ValidateLuhn(orderNumber) {
		http.Error(w, "Invalid order number format", http.StatusUnprocessableEntity)
		return
	}

	// Проверяем, не загружал ли этот заказ пользователь ранее
	order, err := h.orderSvc.GetOrderByNumber(r.Context(), orderNumber)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to get order")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if order != nil {
		if order.UserID == userID {
			w.WriteHeader(http.StatusOK)
			return
		} else {
			http.Error(w, "Order already uploaded by another user", http.StatusConflict)
			return
		}
	}

	// Создаем заказ
	if err := h.orderSvc.CreateOrder(r.Context(), orderNumber, userID); err != nil {
		h.logger.Error().Err(err).Msg("Failed to create order")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) GetOrdersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем аутентификацию пользователя
	userID, ok := middleware.GetUserIDFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем заказы пользователя
	orders, err := h.orderSvc.GetOrdersByUserID(r.Context(), userID)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to get orders")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Если заказов нет, возвращаем 204
	if len(orders) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(orders)
}

func (h *Handler) GetBalanceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем аутентификацию пользователя
	userID, ok := middleware.GetUserIDFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем баланс пользователя
	balance, err := h.orderSvc.GetBalanceByUserID(r.Context(), userID)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to get balance")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(balance)
}

func (h *Handler) CreateWithdrawalHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем аутентификацию пользователя
	userID, ok := middleware.GetUserIDFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Order string  `json:"order"`
		Sum   float64 `json:"sum"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if req.Order == "" || req.Sum <= 0 {
		http.Error(w, "Invalid order or sum", http.StatusBadRequest)
		return
	}

	// Проверяем номер заказа по алгоритму Луна
	if !intmodel.ValidateLuhn(req.Order) {
		http.Error(w, "Invalid order number format", http.StatusUnprocessableEntity)
		return
	}

	// Создаем списание
	err := h.orderSvc.CreateWithdrawal(r.Context(), req.Order, req.Sum, userID)
	if err != nil {
		if err.Error() == "insufficient funds" {
			http.Error(w, err.Error(), http.StatusPaymentRequired)
			return
		}
		h.logger.Error().Err(err).Msg("Failed to create withdrawal")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) GetWithdrawalsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем аутентификацию пользователя
	userID, ok := middleware.GetUserIDFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем списания пользователя
	withdrawals, err := h.orderSvc.GetWithdrawalsByUserID(r.Context(), userID)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to get withdrawals")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Если списаний нет, возвращаем 204
	if len(withdrawals) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(withdrawals)
}

// AuthMiddleware возвращает middleware-обработчик для проверки аутентификации пользователя.
func (h *Handler) AuthMiddleware() *middleware.AuthMiddleware {
	return middleware.NewAuthMiddleware(h.authSvc.GetJWTAuth(), h.logger)
}
