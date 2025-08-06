package internal

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/govalues/decimal"
	_ "github.com/lib/pq"
)

// DBClient is the interface to interact with the database for payment requests.
type DBClient interface {
	Write(PaymentRequest) error
	Read(init, end time.Time) [2]Summary
	Close() error
}

// Summary represents the summary of payment requests and amounts.
type Summary struct {
	TotalRequests int
	TotalAmount   decimal.Decimal
}

type ProcessorType int

const (
	Default ProcessorType = iota
	Fallback
)

type PostgresDBClient struct {
	db *sql.DB
}

func NewPostgresDBClient(connectionString string) (*PostgresDBClient, error) {
	// Open a new database connection
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// connection pool settings
	db.SetMaxOpenConns(35)                 // Maximum number of simultaneous connections
	db.SetMaxIdleConns(20)                 // Maximum number of idle connections
	db.SetConnMaxLifetime(5 * time.Minute) // Maximum lifetime of a connection in the pool

	// ping the database to ensure connection is established
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create the payments table if it does not exist
	return &PostgresDBClient{db: db}, nil
}

func (p *PostgresDBClient) Write(request PaymentRequest) error {
	// Query to insert a new payment record
	query := `
        INSERT INTO payments (correlationId, amount, requested_at) 
        VALUES ($1, $2, $3)
        ON CONFLICT (correlationId) DO NOTHING`

	// Execute the query with the provided parameters
	_, err := p.db.Exec(query, request.CorrelationID, request.Amount, request.RequestedAt)
	if err != nil {
		return fmt.Errorf("failed to insert payment: %w", err)
	}

	return nil
}

func (p *PostgresDBClient) Read(init, end time.Time) ([2]Summary, error) {
	var summaries [2]Summary

	// Query to get the total requests and amount for the given time range
	query := `
        SELECT 
            COUNT(*) as total_requests,
            COALESCE(SUM(amount), 0) as total_amount
        FROM payments 
        WHERE requested_at >= $1 AND requested_at <= $2`

	var totalRequests int
	var totalAmountFloat float64

	// Execute the query and scan the results into totalRequests and totalAmountFloat
	err := p.db.QueryRow(query, init, end).Scan(&totalRequests, &totalAmountFloat)
	if err != nil && err != sql.ErrNoRows {
		return summaries, fmt.Errorf("failed to query payments: %w", err)
	}

	// Convert totalAmountFloat to decimal.Decimal
	totalAmount, err := decimal.NewFromFloat64(totalAmountFloat)
	if err != nil {
		return summaries, fmt.Errorf("failed to convert amount to decimal: %w", err)
	}

	summaries[Default] = Summary{
		TotalRequests: totalRequests,
		TotalAmount:   totalAmount,
	}

	summaries[Fallback] = Summary{
		TotalRequests: 0,
		TotalAmount:   decimal.Zero,
	}

	return summaries, nil
}

func (p *PostgresDBClient) Close() error {
	return p.db.Close()
}

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
