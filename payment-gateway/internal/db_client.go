package internal

import (
	"context"
	"fmt"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// DBClient is the interface to interact with the database for payment requests.
type DBClient interface {
	Write(ctx context.Context, request PaymentRequest) error
	Read(ctx context.Context, init, end time.Time) ([2]Summary, error)
	Close()
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
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		panic(err)
	}

	config.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())

		return nil
	}

	dbpool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		panic(err)
	}

	return &PostgresDBClient{pool: dbpool}
}

func (p *PostgresDBClient) Write(ctx context.Context, request PaymentRequest) error {
	query := `
        INSERT INTO payments (correlationId, amount, typeClient, requested_at) 
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (correlationId) DO NOTHING`

	_, err := p.pool.Exec(ctx, query, request.CorrelationID, request.Amount, int(request.TypeClient), request.RequestedAt)
	if err != nil {
		return fmt.Errorf("failed to insert payment: %w", err)
	}

	return nil
}

func (p *PostgresDBClient) Read(ctx context.Context, init, end time.Time) ([2]Summary, error) {
	var summaries [2]Summary

	query := `
        SELECT 
            typeClient,
            COUNT(*) as total_requests,
            COALESCE(SUM(amount), 0) as total_amount
        FROM payments 
        WHERE requested_at >= $1 AND requested_at <= $2
        GROUP BY typeClient`

	rows, err := p.pool.Query(ctx, query, init, end)
	if err != nil {
		return summaries, fmt.Errorf("query payments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var typ int

		var totalRequests int

		var totalAmount decimal.Decimal

		scanErr := rows.Scan(&typ, &totalRequests, &totalAmount)
		if scanErr != nil {
			return summaries, fmt.Errorf("scan payments: %w", scanErr)
		}

		switch PaymentType(typ) {
		case PaymentTypeDefault:
			summaries[0] = Summary{TotalRequests: totalRequests, TotalAmount: totalAmount}
		case PaymentTypeFallback:
			summaries[1] = Summary{TotalRequests: totalRequests, TotalAmount: totalAmount}
		}
	}

	errRows := rows.Err()
	if errRows != nil {
		return summaries, fmt.Errorf("iterate payments: %w", errRows)
	}

	return summaries, nil
}

// Close closes the underlying database connection.
func (p *PostgresDBClient) Close() {
	if p.pool == nil {
		return
	}

	p.pool.Close()
}
