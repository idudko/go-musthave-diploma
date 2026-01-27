package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	models "github.com/idudko/go-musthave-diploma/internal/model"
	"github.com/idudko/go-musthave-diploma/internal/repository"
)

type AccrualService struct {
	repo          *repository.Repository
	accrualSystem string
}

func NewAccrualService(repo *repository.Repository, accrualSystem string) *AccrualService {
	return &AccrualService{
		repo:          repo,
		accrualSystem: accrualSystem,
	}
}

func (s *AccrualService) ProcessOrder(orderNumber string) {
	// В цикле проверяем статус заказа в системе начисления
	for {
		status, accrual, err := s.getOrderStatus(orderNumber)
		if err != nil {
			fmt.Printf("Failed to get order status: %v\n", err)
			time.Sleep(1 * time.Minute)
			continue
		}

		// Обновляем статус заказа в нашей базе данных
		ctx := context.Background()
		if accrual != nil {
			err = s.repo.UpdateOrderStatus(ctx, orderNumber, status, accrual)
		} else {
			err = s.repo.UpdateOrderStatus(ctx, orderNumber, status, nil)
		}

		if err != nil {
			fmt.Printf("Failed to update order status: %v\n", err)
			time.Sleep(1 * time.Minute)
			continue
		}

		// Если статус окончательный, прекращаем обработку
		if status == "PROCESSED" || status == "INVALID" {
			break
		}

		// Ждем перед следующей проверкой
		time.Sleep(1 * time.Minute)
	}
}

func (s *AccrualService) getOrderStatus(orderNumber string) (string, *float64, error) {
	url := fmt.Sprintf("%s/api/orders/%s", s.accrualSystem, orderNumber)

	resp, err := http.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("failed to request accrual system: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return "NEW", nil, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", nil, fmt.Errorf("rate limit exceeded")
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("accrual system returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var accrualResp models.AccrualResponse
	if err := json.Unmarshal(body, &accrualResp); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return accrualResp.Status, accrualResp.Accrual, nil
}
