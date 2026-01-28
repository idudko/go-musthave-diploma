package models

import (
	"time"
)

type User struct {
	ID        int64     `json:"id"`
	Login     string    `json:"login"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type Order struct {
	ID         int64     `json:"id"`
	Number     string    `json:"number"`
	UserID     int64     `json:"user_id"`
	Status     string    `json:"status"`
	Accrual    *float64  `json:"accrual,omitempty"`
	UploadedAt time.Time `json:"uploaded_at"`
}

type Balance struct {
	Current   float64 `json:"current"`
	Withdrawn float64 `json:"withdrawn"`
}

type Withdrawal struct {
	ID          int64     `json:"id"`
	OrderNumber string    `json:"order"`
	Sum         float64   `json:"sum"`
	UserID      int64     `json:"user_id"`
	ProcessedAt time.Time `json:"processed_at"`
}

type AccrualResponse struct {
	Order   string   `json:"order"`
	Status  string   `json:"status"`
	Accrual *float64 `json:"accrual,omitempty"`
}
