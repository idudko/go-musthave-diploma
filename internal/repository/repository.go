package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	models "github.com/idudko/go-musthave-diploma/internal/model"
)

/*
// Пример использования библиотеки github.com/avast/retry-go для ретраев:
import "github.com/avast/retry-go/v4"

var dbRetryOpts = []retry.Option{
	retry.Attempts(5),                       // Максимум 5 попыток
	retry.Delay(100 * time.Millisecond),     // Начальная задержка 100мс
	retry.MaxDelay(5 * time.Second),         // Максимальная задержка 5с
	retry.MaxJitter(500 * time.Millisecond), // Максимальный jitter 500мс
	retry.LastErrorOnly(true),               // Возвращать только последнюю ошибку
	retry.RetryIf(func(err error) bool {
		return isDBErrorRetryable(err)
	}),
}

// Пример использования:
func (r *Repository) QueryRowWithRetry(ctx context.Context, query string, args ...interface{}) pgx.Row {
	var row pgx.Row
	err := retry.Do(func() error {
		row = r.pool.QueryRow(ctx, query, args...)
		return nil // Для QueryRow ошибка проверяется при Scan
	}, dbRetryOpts...)
	return row
}
*/

// Параметры ретраев для базы данных

// Параметры ретраев для базы данных
const (
	dbMaxRetries   = 5
	dbRetryWaitMin = 100 * time.Millisecond
	dbRetryWaitMax = 5 * time.Second
)

// Repository - обертка над pgxpool.Pool с механизмом ретраев
type Repository struct {
	pool         *pgxpool.Pool
	maxRetries   int
	retryWaitMin time.Duration
	retryWaitMax time.Duration
}

// isDBErrorRetryable проверяет, является ли ошибка БД retryable
func isDBErrorRetryable(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Ошибки подключения
		switch pgErr.Code {
		case "08000", "08001", "08003", "08004", "08006", "08007", // connection exceptions
			"57P01", "57P02", "57P03", // administrator shutdown
			"53300", "53100", "53200": // idle session timeout
			return true
		}

		// Конкурентные транзакции
		switch pgErr.Code {
		case "40001": // serialization failure
		case "40P01": // deadlock detected
			return true
		}
	}

	// Общие сетевые ошибки
	if strings.Contains(err.Error(), "connection timeout") ||
		strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "network is unreachable") ||
		strings.Contains(err.Error(), "connection reset") {
		return true
	}

	// Ошибки временной недоступности
	if strings.Contains(err.Error(), "the database system is starting up") ||
		strings.Contains(err.Error(), "terminating connection") ||
		strings.Contains(err.Error(), "could not connect to server") {
		return true
	}

	// Конкурентные транзакции
	if strings.Contains(err.Error(), "could not serialize access") ||
		strings.Contains(err.Error(), "deadlock detected") {
		return true
	}

	return false
}

// calculateDBWaitTime вычисляет время ожидания с экспоненциальным откатом и jitter для БД
func (r *Repository) calculateDBWaitTime(attempt int) time.Duration {
	// Экспоненциальный откат: base * 2^attempt
	exp := float64(attempt)
	waitMin := float64(r.retryWaitMin)
	waitMax := float64(r.retryWaitMax)

	// Добавляем случайную составляющую (jitter) для предотвращения thundering herd
	random := rand.Float64() * 0.2 // 20% разброс для БД

	// Вычисляем время ожидания
	waitTime := math.Min(waitMin*math.Exp(exp)+random, waitMax)

	return time.Duration(waitTime) * time.Second
}

// QueryRowWithRetry выполняет запрос с ожиданием одной строки и механизмом ретраев
func (r *Repository) QueryRowWithRetry(ctx context.Context, query string, args ...interface{}) pgx.Row {
	for i := 0; i < r.maxRetries; i++ {
		if i > 0 {
			// Вычисляем время ожидания с экспоненциальным откатом и jitter
			wait := r.calculateDBWaitTime(i)

			select {
			case <-time.After(wait):
				// Продолжаем выполнение
			case <-ctx.Done():
				// Контекст был отменен
				return r.pool.QueryRow(ctx, query, args...)
			}
		}

		row := r.pool.QueryRow(ctx, query, args...)
		// Для QueryRow мы не можем сразу проверить ошибку, так как она возникает при Scan
		// Проверку ошибок будем делать в вызывающем коде
		return row
	}

	// Если все попытки завершились неудачно, возвращаем последнюю попытку
	return r.pool.QueryRow(ctx, query, args...)
}

// ExecWithRetry выполняет запрос без возврата данных с механизмом ретраев
func (r *Repository) ExecWithRetry(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	var lastErr error

	for i := 0; i < r.maxRetries; i++ {
		if i > 0 {
			// Вычисляем время ожидания с экспоненциальным откатом и jitter
			wait := r.calculateDBWaitTime(i)

			select {
			case <-time.After(wait):
				// Продолжаем выполнение
			case <-ctx.Done():
				// Контекст был отменен
				return pgconn.NewCommandTag(""), ctx.Err()
			}
		}

		commandTag, err := r.pool.Exec(ctx, query, args...)
		if err == nil {
			return commandTag, nil
		}

		// Сохраняем последнюю ошибку
		lastErr = err

		// Если ошибка не retryable, возвращаем её сразу
		if !isDBErrorRetryable(err) {
			return pgconn.NewCommandTag(""), err
		}
	}

	return pgconn.NewCommandTag(""), fmt.Errorf("after %d retries, last error: %w", r.maxRetries, lastErr)
}

func (r *Repository) CreateUser(ctx context.Context, login, passwordHash string) (int64, error) {
	var id int64
	err := r.QueryRowWithRetry(ctx,
		"INSERT INTO users (login, password) VALUES ($1, $2) RETURNING id",
		login, passwordHash).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to create user: %w", err)
	}
	return id, nil
}

func (r *Repository) GetUserByLogin(ctx context.Context, login string) (*models.User, error) {
	var user models.User
	err := r.QueryRowWithRetry(ctx,
		"SELECT id, login, password FROM users WHERE login = $1",
		login).Scan(&user.ID, &user.Login, &user.Password)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}

func (r *Repository) CreateOrder(ctx context.Context, number string, userID int64) error {
	_, err := r.ExecWithRetry(ctx,
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
	err := r.QueryRowWithRetry(ctx,
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
	// Для Query мы не используем ретраи на уровне метода, так как rows уже предоставляет
	// возможность обработки ошибок при итерации
	rows, err := r.pool.Query(ctx,
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
	err := r.QueryRowWithRetry(ctx,
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
	// Используем транзакцию с уровнем изоляции SERIALIZABLE для предотвращения гонки состояний
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		// В случае ошибки откатываем транзакцию
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Используем подход "сначала создай списание, потом проверь баланс"
	// Это более надежно, так как предотвращает гонку состояний
	_, err = tx.Exec(ctx,
		"INSERT INTO withdrawals (order_number, sum, user_id, processed_at) VALUES ($1, $2, $3, NOW())",
		order, sum, userID)
	if err != nil {
		return fmt.Errorf("failed to create withdrawal: %w", err)
	}

	// Теперь проверим, не стал ли баланс отрицательным после списания
	var balance float64
	err = tx.QueryRow(ctx,
		"SELECT COALESCE(SUM(accrual), 0) - COALESCE((SELECT COALESCE(SUM(sum), 0) FROM withdrawals WHERE user_id = $1), 0) "+
			"FROM (SELECT COALESCE(SUM(accrual), 0) as accrual FROM orders WHERE user_id = $1 AND status = 'PROCESSED') as t",
		userID).Scan(&balance)
	if err != nil {
		return fmt.Errorf("failed to check balance after withdrawal: %w", err)
	}

	// Если баланс отрицательный, откатываем транзакцию и возвращаем ошибку
	if balance < 0 {
		return fmt.Errorf("insufficient funds")
	}

	// Подтверждаем транзакцию только если баланс остался неотрицательным
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (r *Repository) GetWithdrawalsByUserID(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	// Для Query мы не используем ретраи на уровне метода, так как rows уже предоставляет
	// возможность обработки ошибок при итерации
	rows, err := r.pool.Query(ctx,
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

// GetOrdersByStatus возвращает заказы с указанными статусами
func (r *Repository) GetOrdersByStatus(ctx context.Context, statuses []string) ([]models.Order, error) {
	// Для Query мы не используем ретраи на уровне метода, так как rows уже предоставляет
	// возможность обработки ошибок при итерации
	rows, err := r.pool.Query(ctx,
		"SELECT id, number, user_id, status, accrual, uploaded_at FROM orders WHERE status = ANY($1)",
		statuses)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders by status: %w", err)
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

func (r *Repository) UpdateOrderStatus(ctx context.Context, number string, status string, accrual *float64) error {
	if accrual != nil {
		_, err := r.ExecWithRetry(ctx,
			"UPDATE orders SET status = $1, accrual = $2 WHERE number = $3",
			status, *accrual, number)
		if err != nil {
			return fmt.Errorf("failed to update order status with accrual: %w", err)
		}
	} else {
		_, err := r.ExecWithRetry(ctx,
			"UPDATE orders SET status = $1 WHERE number = $2",
			status, number)
		if err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}
	}
	return nil
}
