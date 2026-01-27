package main

import (
	"context"
	"net/http"

	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/idudko/go-musthave-diploma/internal/config"
	inthandler "github.com/idudko/go-musthave-diploma/internal/handler"
	"github.com/idudko/go-musthave-diploma/internal/repository"
	intservice "github.com/idudko/go-musthave-diploma/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Загружаем конфигурацию
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to load config")
	}

	// Подключаемся к базе данных
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURI)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer pool.Close()

	// Выполняем миграции
	if err := repository.RunMigrations(cfg.DatabaseURI); err != nil {
		logger.Fatal().Err(err).Msg("Failed to run migrations")
	}

	// Создаем репозиторий
	repo := repository.NewRepository(pool)

	// Создаем сервисы
	authSvc := intservice.NewAuthService(repo)
	accrualSvc := intservice.NewAccrualService(repo, cfg.AccrualSystemAddress)
	orderSvc := intservice.NewOrderService(repo, accrualSvc)

	// Создаем обработчики
	h := inthandler.NewHandler(authSvc, orderSvc, accrualSvc, &logger)

	// Создаем роутер
	r := chi.NewRouter()

	// Регистрируем обработчики для регистрации и входа (без аутентификации)
	r.Post("/api/user/register", h.RegisterUserHandler)
	r.Post("/api/user/login", h.LoginUserHandler)

	// Группа роутов, требующих аутентификации
	r.Group(func(router chi.Router) {
		// Используем middleware для проверки JWT
		router.Use(h.AuthMiddleware().Middleware)

		router.Post("/api/user/orders", h.UploadOrderHandler)
		router.Get("/api/user/orders", h.GetOrdersHandler)
		router.Get("/api/user/balance", h.GetBalanceHandler)
		router.Post("/api/user/balance/withdraw", h.CreateWithdrawalHandler)
		router.Get("/api/user/withdrawals", h.GetWithdrawalsHandler)
	})

	// Создаем HTTP сервер
	srv := &http.Server{
		Addr:    cfg.RunAddress,
		Handler: r,
	}

	// Запускаем сервер в горутине
	go func() {
		logger.Info().Msgf("Starting server on %s", cfg.RunAddress)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Настраиваем graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutting down server...")

	// Даем время на завершение текущих запросов
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal().Err(err).Msg("Server forced to shutdown")
	}

	logger.Info().Msg("Server exited")
}
