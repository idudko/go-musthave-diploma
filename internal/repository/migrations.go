package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RunMigrations(dsn string) error {
	// Create a new migrate instance using the SQL files in the migrations directory
	m, err := migrate.New("file://migrations", dsn)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// Apply all available migrations
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Log the migration status
	if err == migrate.ErrNoChange {
		log.Println("Database is up to date")
	} else {
		log.Println("Database migrations completed successfully")
	}

	return nil
}

// ConnectWithMigrations создает подключение к базе данных и выполняет миграции
func ConnectWithMigrations(dsn string) (*pgxpool.Pool, error) {
	// Сначала выполняем миграции
	if err := RunMigrations(dsn); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Создаем пул соединений с базой данных
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	return pool, nil
}
