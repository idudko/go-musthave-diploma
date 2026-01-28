package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	models "github.com/idudko/go-musthave-diploma/internal/model"
	"github.com/idudko/go-musthave-diploma/internal/repository"
)

const (
	defaultWorkersCount = 5
	defaultPollInterval = 1 * time.Second
	maxRetries          = 3
	retryWaitMin        = 1 * time.Second
	retryWaitMax        = 30 * time.Second
)

// Get выполняет HTTP GET запрос с механизмом ретраев
func (c *RetryableHTTPClient) Get(url string) (*http.Response, error) {
	var resp *http.Response
	var err error

	for i := 0; i < c.maxRetries; i++ {
		if i > 0 {
			// Вычисляем время ожидания с экспоненциальным откатом и jitter
			wait := c.calculateWaitTime(i)
			time.Sleep(wait)
		}

		resp, err = c.client.Get(url)
		if err != nil {
			// Проверяем, является ли ошибка retryable
			if !IsRetryableError(err) {
				return nil, err
			}
			continue
		}

		// Проверяем, нужно ли повторить запрос на основе статуса
		if shouldRetry(resp) {
			resp.Body.Close()
			continue
		}

		// Если всё хорошо, возвращаем ответ
		return resp, nil
	}

	// Если все попытки завершились неудачно
	if resp != nil {
		resp.Body.Close()
	}
	return nil, fmt.Errorf("after %d retries, last error: %v", c.maxRetries, err)
}

// calculateWaitTime вычисляет время ожидания с экспоненциальным откатом и jitter
func (c *RetryableHTTPClient) calculateWaitTime(attempt int) time.Duration {
	// Экспоненциальный откат: base * 2^attempt
	exp := float64(attempt)
	waitMin := float64(c.retryWaitMin)
	waitMax := float64(c.retryWaitMax)

	// Добавляем случайную составляющую (jitter) для предотвращения thundering herd
	random := rand.Float64() * 0.3 // 30% разброс

	// Вычисляем время ожидания
	waitTime := math.Min(waitMin*math.Exp(exp)+random, waitMax)

	return time.Duration(waitTime) * time.Second
}

// isRetryableError проверяет, является ли ошибка retryable
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Преобразуем ошибку в строку для проверки
	errStr := err.Error()

	// Проверяем на сетевые ошибки
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "connection reset") {
		return true
	}

	// Проверяем на ошибки временной недоступности
	if strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "temporary error") {
		return true
	}

	// Проверяем на ошибки URL
	if urlErr, ok := err.(*url.Error); ok {
		// Timeout ошибки retryable
		if urlErr.Timeout() {
			return true
		}
	}

	return false
}

// shouldRetry определяет, нужно ли повторить запрос на основе статуса ответа
func shouldRetry(resp *http.Response) bool {
	if resp == nil {
		return false
	}

	// Повторяем при ошибках сервера 5xx, кроме 501 (Not Implemented)
	statusCode := resp.StatusCode
	if statusCode >= 500 && statusCode != 501 {
		return true
	}

	// Также повторяем при ошибке 429 (Too Many Requests)
	if statusCode == http.StatusTooManyRequests {
		return true
	}

	return false
}

// IsRetryableError проверяет, является ли ошибка retryable

// RetryableHTTPClient - обертка над http-клиентом с механизмом ретраев
type RetryableHTTPClient struct {
	client       *http.Client
	maxRetries   int
	retryWaitMin time.Duration
	retryWaitMax time.Duration
}

// NewRetryableHTTPClient создает новый экземпляр клиента с ретраями
func NewRetryableHTTPClient() *RetryableHTTPClient {
	return &RetryableHTTPClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxRetries:   maxRetries,
		retryWaitMin: retryWaitMin,
		retryWaitMax: retryWaitMax,
	}
}

// Объявляем функцию ShouldRetry на уровне пакета для использования в других частях кода
func ShouldRetry(resp *http.Response) bool {
	if resp == nil {
		return false
	}

	// Повторяем при ошибках сервера 5xx, кроме 501 (Not Implemented)
	statusCode := resp.StatusCode
	if statusCode >= 500 && statusCode != 501 {
		return true
	}

	// Также повторяем при ошибке 429 (Too Many Requests)
	if statusCode == http.StatusTooManyRequests {
		return true
	}

	return false
}

type AccrualService struct {
	repo          *repository.Repository
	accrualSystem string
	rateLimit     *RateLimitManager
	workerPool    *WorkerPool
	ctx           context.Context
	cancel        context.CancelFunc
	httpClient    *RetryableHTTPClient
}

// OrderTask представляет задачу для обработки заказа
type OrderTask struct {
	OrderNumber string
	UpdatedAt   time.Time
}

// RateLimitManager управляет состоянием rate limiting для всех воркеров
type RateLimitManager struct {
	mu           sync.RWMutex
	limitReached bool
	retryAfter   time.Time
}

// WorkerPool управляет пулом воркеров для обработки заказов
type WorkerPool struct {
	ctx         context.Context
	taskQueue   chan OrderTask
	workerCount int
	repo        *repository.Repository
	accrualSvc  *AccrualService
	rateLimit   *RateLimitManager
}

func NewAccrualService(repo *repository.Repository, accrualSystem string) *AccrualService {
	ctx, cancel := context.WithCancel(context.Background())

	service := &AccrualService{
		repo:          repo,
		accrualSystem: accrualSystem,
		rateLimit:     &RateLimitManager{},
		ctx:           ctx,
		cancel:        cancel,
		httpClient:    NewRetryableHTTPClient(),
	}

	service.workerPool = NewWorkerPool(ctx, repo, service, service.rateLimit)

	return service
}

// NewWorkerPool создает новый пул воркеров
func NewWorkerPool(ctx context.Context, repo *repository.Repository, accrualSvc *AccrualService, rateLimit *RateLimitManager) *WorkerPool {
	wp := &WorkerPool{
		ctx:         ctx,
		taskQueue:   make(chan OrderTask, 100),
		workerCount: defaultWorkersCount,
		repo:        repo,
		accrualSvc:  accrualSvc,
		rateLimit:   rateLimit,
	}

	// Запускаем воркеры
	for i := 0; i < wp.workerCount; i++ {
		go wp.worker(i)
	}

	return wp
}

// Start запускает обработку заказов
func (s *AccrualService) Start() {
	// Получаем все незавершенные заказы и добавляем их в очередь
	orders, err := s.repo.GetOrdersByStatus(s.ctx, []string{"NEW", "PROCESSING"})
	if err != nil {
		fmt.Printf("Failed to get unprocessed orders: %v\n", err)
		return
	}

	for _, order := range orders {
		s.workerPool.AddTask(OrderTask{
			OrderNumber: order.Number,
			UpdatedAt:   time.Now(),
		})
	}
}

// Stop останавливает обработку заказов
func (s *AccrualService) Stop() {
	s.cancel()
}

// AddTask добавляет задачу в очередь воркеров
func (s *AccrualService) AddTask(orderNumber string) {
	s.workerPool.AddTask(OrderTask{
		OrderNumber: orderNumber,
		UpdatedAt:   time.Now(),
	})
}

// worker обрабатывает задачи из очереди
func (wp *WorkerPool) worker(id int) {
	fmt.Printf("Worker %d started\n", id)
	defer fmt.Printf("Worker %d stopped\n", id)

	for {
		select {
		case <-wp.ctx.Done():
			return
		case task, ok := <-wp.taskQueue:
			if !ok {
				return
			}

			wp.processTask(task, id)
		}
	}
}

// processTask обрабатывает одну задачу
func (wp *WorkerPool) processTask(task OrderTask, workerID int) {
	// Проверяем, не достигнут ли rate limit
	if limited, duration := wp.rateLimit.IsRateLimited(); limited {
		fmt.Printf("Worker %d: Rate limit reached. Waiting for %v before retrying order %s\n", workerID, duration, task.OrderNumber)

		select {
		case <-time.After(duration):
			// Период ожидания завершен, сбрасываем состояние rate limiting
			wp.rateLimit.ResetRateLimit()
		case <-wp.ctx.Done():
			// Контекст был отменен, завершаем работу
			fmt.Printf("Worker %d: Stopping order processing for %s: context canceled\n", workerID, task.OrderNumber)
			return
		}
	}

	// Проверяем статус заказа в accrual системе
	status, accrual, sleepDuration, err := wp.accrualSvc.getOrderStatusWithRateLimit(task.OrderNumber)
	if err != nil {
		fmt.Printf("Worker %d: Failed to get order status for %s: %v\n", workerID, task.OrderNumber, err)

		// Если получена ошибка 429, устанавливаем состояние rate limiting
		if sleepDuration > 0 {
			wp.rateLimit.SetRateLimit(time.Now().Add(sleepDuration))
			fmt.Printf("Worker %d: Rate limit reached. All workers will sleep for %v\n", workerID, sleepDuration)
			// Возвращаем задачу в очередь для повторной обработки после окончания периода rate limiting
			wp.AddTask(task)
			return
		}

		// Проверяем, является ли ошибка retryable
		if strings.Contains(err.Error(), "retryable error") {
			// Для retryable ошибок просто возвращаем задачу в очередь
			// HTTP клиент уже выполнил необходимое количество ретраев
			wp.AddTask(task)
			return
		}

		// Для других ошибок также возвращаем задачу в очередь
		wp.AddTask(task)
		return
	}

	// Обновляем статус заказа в нашей базе данных
	if accrual != nil {
		err = wp.repo.UpdateOrderStatus(wp.ctx, task.OrderNumber, status, accrual)
	} else {
		err = wp.repo.UpdateOrderStatus(wp.ctx, task.OrderNumber, status, nil)
	}

	if err != nil {
		fmt.Printf("Worker %d: Failed to update order status for %s: %v\n", workerID, task.OrderNumber, err)
		// Возвращаем задачу в очередь для повторной обработки
		wp.AddTask(task)
		return
	}

	// Если статус окончательный, прекращаем обработку
	if status == "PROCESSED" || status == "INVALID" {
		fmt.Printf("Worker %d: Order %s processing completed with status %s\n", workerID, task.OrderNumber, status)
		return
	}

	// Если статус не окончательный, добавляем задачу обратно в очередь для повторной проверки
	select {
	case wp.taskQueue <- OrderTask{
		OrderNumber: task.OrderNumber,
		UpdatedAt:   time.Now(),
	}:
	case <-wp.ctx.Done():
		fmt.Printf("Worker %d: Stopping order processing for %s: context canceled\n", workerID, task.OrderNumber)
	}
}

// AddTask добавляет задачу в очередь воркеров
func (wp *WorkerPool) AddTask(task OrderTask) {
	select {
	case wp.taskQueue <- task:
	case <-wp.ctx.Done():
		fmt.Printf("Failed to add task %s to queue: context canceled\n", task.OrderNumber)
	}
}

// SetRateLimit устанавливает состояние rate limiting
func (rlm *RateLimitManager) SetRateLimit(retryAfter time.Time) {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()

	rlm.limitReached = true
	rlm.retryAfter = retryAfter
}

// IsRateLimited проверяет, достигнут ли лимит запросов
func (rlm *RateLimitManager) IsRateLimited() (bool, time.Duration) {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()

	if !rlm.limitReached {
		return false, 0
	}

	now := time.Now()
	if now.After(rlm.retryAfter) {
		return false, 0
	}

	return true, rlm.retryAfter.Sub(now)
}

// ResetRateLimit сбрасывает состояние rate limiting
func (rlm *RateLimitManager) ResetRateLimit() {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()

	rlm.limitReached = false
}

// ProcessOrder добавляет заказ в очередь обработки
func (s *AccrualService) ProcessOrder(ctx context.Context, orderNumber string) {
	s.AddTask(orderNumber)
}

func (s *AccrualService) getOrderStatus(orderNumber string) (string, *float64, error) {
	status, _, _, err := s.getOrderStatusWithRateLimit(orderNumber)
	return status, nil, err
}

func (s *AccrualService) getOrderStatusWithRateLimit(orderNumber string) (string, *float64, time.Duration, error) {
	url := fmt.Sprintf("%s/api/orders/%s", s.accrualSystem, orderNumber)

	resp, err := s.httpClient.Get(url)
	if err != nil {
		return "", nil, 0, fmt.Errorf("failed to request accrual system: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return "NEW", nil, 0, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		// Извлекаем время ожидания из заголовка Retry-After
		var sleepDuration time.Duration

		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				sleepDuration = time.Duration(seconds) * time.Second
			} else {
				// Если не удалось распарсить число, используем значение по умолчанию
				sleepDuration = 60 * time.Second
			}
		} else {
			// Если заголовок отсутствует, используем значение по умолчанию
			sleepDuration = 60 * time.Second
		}

		return "", nil, sleepDuration, fmt.Errorf("rate limit exceeded")
	}

	// Проверяем, нужно ли повторить запрос на основе статуса
	if shouldRetry(resp) {
		// Для ошибок 5xx возвращаем специальную ошибку, которая будет обработана в worker
		return "", nil, 0, fmt.Errorf("retryable error: status %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, 0, fmt.Errorf("accrual system returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, 0, fmt.Errorf("failed to read response body: %w", err)
	}

	var accrualResp models.AccrualResponse
	if err := json.Unmarshal(body, &accrualResp); err != nil {
		return "", nil, 0, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return accrualResp.Status, accrualResp.Accrual, 0, nil
}
