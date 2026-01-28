package services

import (
	"context"
	"fmt"

	models "github.com/idudko/go-musthave-diploma/internal/model"
	"github.com/idudko/go-musthave-diploma/internal/repository"
	"github.com/idudko/go-musthave-diploma/internal/utils"
)

type OrderService struct {
	repo       *repository.Repository
	accrualSvc *AccrualService
}

func NewOrderService(repo *repository.Repository, accrualSvc *AccrualService) *OrderService {
	return &OrderService{
		repo:       repo,
		accrualSvc: accrualSvc,
	}
}

func (s *OrderService) CreateOrder(ctx context.Context, number string, userID int64) error {
	// Проверяем номер заказа по алгоритму Луна
	if !utils.CheckLuhn(number) {
		return fmt.Errorf("invalid order number format")
	}

	// Проверяем, не загружал ли этот заказ другой пользователь
	order, err := s.repo.GetOrderByNumber(ctx, number)
	if err != nil {
		return fmt.Errorf("failed to check order existence: %w", err)
	}
	if order != nil {
		if order.UserID == userID {
			return fmt.Errorf("order already uploaded by this user")
		}
		return fmt.Errorf("order already uploaded by another user")
	}

	// Создаем заказ
	if err := s.repo.CreateOrder(ctx, number, userID); err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}

	// Асинхронно отправляем заказ на обработку в систему начисления баллов
	go s.accrualSvc.ProcessOrder(number)

	return nil
}

func (s *OrderService) GetOrdersByUserID(ctx context.Context, userID int64) ([]models.Order, error) {
	orders, err := s.repo.GetOrdersByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders: %w", err)
	}
	return orders, nil
}

// GetOrderByNumber возвращает заказ по номеру
func (s *OrderService) GetOrderByNumber(ctx context.Context, number string) (*models.Order, error) {
	order, err := s.repo.GetOrderByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}
	return order, nil
}

func (s *OrderService) GetBalanceByUserID(ctx context.Context, userID int64) (*models.Balance, error) {
	balance, err := s.repo.GetBalanceByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}
	return balance, nil
}

func (s *OrderService) CreateWithdrawal(ctx context.Context, order string, sum float64, userID int64) error {
	// Проверяем номер заказа по алгоритму Луна
	if !utils.CheckLuhn(order) {
		return fmt.Errorf("invalid order number format")
	}

	// Создаем списание
	// Проверка баланса и списание выполняются в одной транзакции в репозитории
	// для предотвращения race condition и обеспечения консистентности данных
	if err := s.repo.CreateWithdrawal(ctx, order, sum, userID); err != nil {
		return fmt.Errorf("failed to create withdrawal: %w", err)
	}

	return nil
}

func (s *OrderService) GetWithdrawalsByUserID(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	withdrawals, err := s.repo.GetWithdrawalsByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get withdrawals: %w", err)
	}
	return withdrawals, nil
}
