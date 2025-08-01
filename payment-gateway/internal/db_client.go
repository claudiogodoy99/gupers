package internal

import (
	"time"

	"github.com/govalues/decimal"
)

// DBClient is the interface to interact with the database for payment requests.
type DBClient interface {
	write(request PaymentRequest) error
	read(init, end time.Time) [2]Summary
}

// Summary represents the summary of payment requests and amounts.
type Summary struct {
	TotalRequests int
	TotalAmount   decimal.Decimal
}

// expects
// read: HTTP 200 - Ok
// {
//     "default" : {
//         "totalRequests": 43236,
//         "totalAmount": 415542345.98
//     },
//     "fallback" : {
//         "totalRequests": 423545,
//         "totalAmount": 329347.34
//     }
// }

// write: err ou nil
