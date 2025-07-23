package internal

import (
	"time"

	"github.com/govalues/decimal"
)

type DBClient interface {
	write(PaymentRequest) error
	read(init, end time.Time) [2]Summary
}

type Summary struct {
	TotalRequests int
	TotalAmount   decimal.Decimal
}

type ProcessorType int

const (
	Default ProcessorType = iota
	Fallback
)

//expects
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
