package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
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
	pool *pgxpool.Pool
}

// NewPostgresDBClient Constructor PostgresDBClient.
func NewPostgresDBClient(ctx context.Context, connectionString string) *PostgresDBClient {
	dbpool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		panic(fmt.Sprintf("Unable to create connection pool: %v\n", err))
	}

	return &PostgresDBClient{pool: dbpool}
}

func (p *PostgresDBClient) Write(ctx context.Context, request PaymentRequest) error {
	query := `
        INSERT INTO payments (correlationId, amount, requested_at) 
        VALUES ($1, $2, $3)
        ON CONFLICT (correlationId) DO NOTHING`

	_, err := p.pool.Exec(ctx, query, request.CorrelationID, request.Amount, request.RequestedAt)
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

	var totalAmount decimal.Decimal

	err := p.pool.QueryRow(ctx, query, init, end).Scan(&totalRequests, &totalAmount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return summaries, fmt.Errorf("query payments: %w", err)
	}

	summaries[0] = Summary{TotalRequests: totalRequests, TotalAmount: totalAmount}
	summaries[1] = Summary{TotalRequests: 0, TotalAmount: decimal.Zero}

	return summaries, nil
}

// Close closes the underlying database connection.
func (p *PostgresDBClient) Close() error {
	if p.pool == nil {
		return nil
	}

	return fmt.Errorf("close db: %w", p.Close())
}
