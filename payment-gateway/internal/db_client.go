package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/govalues/decimal"
)

// DBClient is the interface to interact with the database for payment requests.
type DBClient interface {
	Write(ctx context.Context, request PaymentRequest) error
	Read(ctx context.Context, init, end time.Time) ([2]Summary, error)
	Close() error
}

// Summary represents the summary of payment requests and amounts.
type Summary struct {
	TotalRequests int
	TotalAmount   decimal.Decimal
}

// PostgresDBClient represents the client connection.
type PostgresDBClient struct {
	db *sql.DB
}

const (
	maxOpenConns    = 35
	maxIdleConns    = 20
	connMaxLifetime = 5 * time.Minute
	pingMaxRetries  = 10
	pingBaseDelay   = time.Second
)

// NewPostgresDBClient Constructor PostgresDBClient.
func NewPostgresDBClient(ctx context.Context, connectionString string) (*PostgresDBClient, error) {
	sqlDB, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	var pingErr error
	for attempt := 1; attempt <= pingMaxRetries; attempt++ {
		pingErr = sqlDB.PingContext(ctx)
		if pingErr == nil {
			break
		}

		if attempt == pingMaxRetries {
			return nil, fmt.Errorf("ping database after %d attempts: %w", pingMaxRetries, pingErr)
		}

		time.Sleep(time.Duration(attempt) * pingBaseDelay)
	}

	return &PostgresDBClient{db: sqlDB}, nil
}

func (p *PostgresDBClient) Write(ctx context.Context, request PaymentRequest) error {
	query := `
        INSERT INTO payments (correlationId, amount, requested_at) 
        VALUES ($1, $2, $3)
        ON CONFLICT (correlationId) DO NOTHING`

	_, err := p.db.ExecContext(ctx, query, request.CorrelationID, request.Amount, request.RequestedAt)
	if err != nil {
		return fmt.Errorf("failed to insert payment: %w", err)
	}

	return nil
}

func (p *PostgresDBClient) Read(ctx context.Context, init, end time.Time) ([2]Summary, error) {
	var summaries [2]Summary

	query := `
        SELECT 
            COUNT(*) as total_requests,
            COALESCE(SUM(amount), 0) as total_amount
        FROM payments 
        WHERE requested_at >= $1 AND requested_at <= $2`

	var totalRequests int

	var totalAmountFloat64 float64

	err := p.db.QueryRowContext(ctx, query, init, end).Scan(&totalRequests, &totalAmountFloat64)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return summaries, fmt.Errorf("query payments: %w", err)
	}

	amountDecimal, err := decimal.NewFromFloat64(totalAmountFloat64)
	if err != nil {
		return summaries, fmt.Errorf("amount to decimal: %w", err)
	}

	summaries[0] = Summary{TotalRequests: totalRequests, TotalAmount: amountDecimal}
	summaries[1] = Summary{TotalRequests: 0, TotalAmount: decimal.Zero}

	return summaries, nil
}

// Close closes the underlying database connection.
func (p *PostgresDBClient) Close() error {
	if p.db == nil {
		return nil
	}

	return fmt.Errorf("close db: %w", p.db.Close())
}
