package internal

import (
	"time"

	"github.com/govalues/decimal"
)

type DBClient interface {
	write(PaymentRequest) error
	read(init, end time.Time) []Summary
}

type Summary struct {
	TotalRequests int
	TotalAmount   decimal.Decimal
}
