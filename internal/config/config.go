package config

import (
	"fmt"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	RunAddress           string `env:"RUN_ADDRESS" env-default:"localhost:8080"`
	DatabaseURI          string `env:"DATABASE_URI" env-default:"postgres://postgres:postgres@localhost:5432/gophermart?sslmode=disable"`
	AccrualSystemAddress string `env:"ACCRUAL_SYSTEM_ADDRESS" env-default:"http://localhost:8081"`
}

func LoadConfig() (*Config, error) {
	var cfg Config

	// Проверяем наличие переменных окружения
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	if cfg.RunAddress == "" {
		cfg.RunAddress = "localhost:8080"
	}
	if cfg.DatabaseURI == "" {
		cfg.DatabaseURI = "postgres://postgres:postgres@localhost:5432/gophermart?sslmode=disable"
	}
	if cfg.AccrualSystemAddress == "" {
		cfg.AccrualSystemAddress = "http://localhost:8081"
	}

	return &cfg, nil
}

func PrintConfig(cfg *Config) {
	fmt.Printf("Server Address: %s\n", cfg.RunAddress)
	fmt.Printf("Database URI: %s\n", cfg.DatabaseURI)
	fmt.Printf("Accrual System Address: %s\n", cfg.AccrualSystemAddress)
}
