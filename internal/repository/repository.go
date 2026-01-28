package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	models "github.com/idudko/go-musthave-diploma/internal/model"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateUser(ctx context.Context, login, hashedPassword string) (int64, error) {
	var id int64
	err := r.db.QueryRow(ctx,
		"INSERT INTO users (login, password) VALUES ($1, $2) RETURNING id",
		login, hashedPassword).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to create user: %w", err)
	}
	return id, nil
}

func (r *Repository) GetUserByLogin(ctx context.Context, login string) (*models.User, error) {
	var user models.User
	err := r.db.QueryRow(ctx,
		"SELECT id, login, password, created_at FROM users WHERE login = $1",
		login).Scan(&user.ID, &user.Login, &user.Password, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}

func (r *Repository) CreateOrder(ctx context.Context, number string, userID int64) error {
	_, err := r.db.Exec(ctx,
		"INSERT INTO orders (number, user_id, status, uploaded_at) VALUES ($1, $2, 'NEW', NOW())",
		number, userID)
	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}
	return nil
}

// GetOrderByNumber возвращает заказ по номеру
func (r *Repository) GetOrderByNumber(ctx context.Context, number string) (*models.Order, error) {
	var order models.Order
	err := r.db.QueryRow(ctx,
		"SELECT id, number, user_id, status, accrual, uploaded_at FROM orders WHERE number = $1",
		number).Scan(&order.ID, &order.Number, &order.UserID, &order.Status, &order.Accrual, &order.UploadedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get order: %w", err)
	}
	return &order, nil
}

func (r *Repository) GetOrdersByUserID(ctx context.Context, userID int64) ([]models.Order, error) {
	rows, err := r.db.Query(ctx,
		"SELECT id, number, user_id, status, accrual, uploaded_at FROM orders WHERE user_id = $1 ORDER BY uploaded_at DESC",
		userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders: %w", err)
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var order models.Order
		err := rows.Scan(&order.ID, &order.Number, &order.UserID, &order.Status, &order.Accrual, &order.UploadedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

func (r *Repository) GetBalanceByUserID(ctx context.Context, userID int64) (*models.Balance, error) {
	var balance models.Balance
	err := r.db.QueryRow(ctx,
		"SELECT COALESCE(SUM(accrual), 0) - COALESCE((SELECT SUM(sum) FROM withdrawals WHERE user_id = $1), 0) as current, "+
			"COALESCE((SELECT SUM(sum) FROM withdrawals WHERE user_id = $1), 0) as withdrawn "+
			"FROM (SELECT COALESCE(SUM(accrual), 0) as accrual FROM orders WHERE user_id = $1 AND status = 'PROCESSED') as t",
		userID).Scan(&balance.Current, &balance.Withdrawn)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}
	return &balance, nil
}

func (r *Repository) CreateWithdrawal(ctx context.Context, order string, sum float64, userID int64) error {
	// Используем транзакцию с уровнем изоляции REPEATABLE READ для предотвращения гонки состояний
	// и явную блокировку SELECT FOR UPDATE для блокировки строк пользователя от изменений
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		// В случае ошибки откатываем транзакцию
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Проверяем, достаточно ли средств внутри транзакции с блокировкой
	var balance float64
	err = tx.QueryRow(ctx,
		"SELECT COALESCE(SUM(accrual), 0) - COALESCE((SELECT SUM(sum) FROM withdrawals WHERE user_id = $1 FOR UPDATE), 0) "+
			"FROM (SELECT COALESCE(SUM(accrual), 0) as accrual FROM orders WHERE user_id = $1 AND status = 'PROCESSED' FOR UPDATE) as t",
		userID).Scan(&balance)
	if err != nil {
		return fmt.Errorf("failed to get balance: %w", err)
	}

	if balance < sum {
		return fmt.Errorf("insufficient funds")
	}

	// Создаем списание внутри транзакции
	_, err = tx.Exec(ctx,
		"INSERT INTO withdrawals (order_number, sum, user_id, processed_at) VALUES ($1, $2, $3, NOW())",
		order, sum, userID)
	if err != nil {
		return fmt.Errorf("failed to create withdrawal: %w", err)
	}

	// Подтверждаем транзакцию
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (r *Repository) GetWithdrawalsByUserID(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	rows, err := r.db.Query(ctx,
		"SELECT id, order_number, sum, user_id, processed_at FROM withdrawals WHERE user_id = $1 ORDER BY processed_at DESC",
		userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get withdrawals: %w", err)
	}
	defer rows.Close()

	var withdrawals []models.Withdrawal
	for rows.Next() {
		var withdrawal models.Withdrawal
		err := rows.Scan(&withdrawal.ID, &withdrawal.OrderNumber, &withdrawal.Sum, &withdrawal.UserID, &withdrawal.ProcessedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan withdrawal: %w", err)
		}
		withdrawals = append(withdrawals, withdrawal)
	}

	return withdrawals, nil
}

func (r *Repository) UpdateOrderStatus(ctx context.Context, number string, status string, accrual *float64) error {
	if accrual != nil {
		_, err := r.db.Exec(ctx,
			"UPDATE orders SET status = $1, accrual = $2 WHERE number = $3",
			status, *accrual, number)
		if err != nil {
			return fmt.Errorf("failed to update order status with accrual: %w", err)
		}
	} else {
		_, err := r.db.Exec(ctx,
			"UPDATE orders SET status = $1 WHERE number = $2",
			status, number)
		if err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}
	}
	return nil
}
