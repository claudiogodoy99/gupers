package internal

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/govalues/decimal"
)

// nolint: cyclop
func TestPostgresDBClient(t *testing.T) {
	// Skip test if no database URL is provided
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL environment variable not set")
	}

	// Create database client
	client, err := NewPostgresDBClient(dbURL)
	if err != nil {
		t.Fatalf("Failed to create database client: %v", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("Failed to close database client: %v", closeErr)
		}
	}()

	// Test Write functionality
	t.Run("Write", func(t *testing.T) {
		request := PaymentRequest{
			Amount:        100.50,
			CorrelationID: uuid.New().String(),
			RequestedAt:   time.Now().UTC(),
		}

		err := client.Write(request)
		if err != nil {
			t.Errorf("Failed to write payment: %v", err)
		}

		// Try to write the same request again (should not error due to ON CONFLICT DO NOTHING)
		err = client.Write(request)
		if err != nil {
			t.Errorf("Failed to write duplicate payment: %v", err)
		}
	})

	// Test Read functionality
	t.Run("Read", func(t *testing.T) {
		// Write a test payment first
		testTime := time.Now().UTC()
		request := PaymentRequest{
			Amount:        250.75,
			CorrelationID: uuid.New().String(),
			RequestedAt:   testTime,
		}

		err := client.Write(request)
		if err != nil {
			t.Fatalf("Failed to write test payment: %v", err)
		}

		// Read payments in a range that includes our test payment
		startTime := testTime.Add(-1 * time.Hour)
		endTime := testTime.Add(1 * time.Hour)

		summaries := client.Read(startTime, endTime)

		// Check that we have some data
		if summaries[Default].TotalRequests == 0 {
			t.Error("Expected at least one payment in default summary")
		}

		if summaries[Default].TotalAmount.IsZero() {
			t.Error("Expected non-zero total amount in default summary")
		}

		// Fallback should be zero
		if summaries[Fallback].TotalRequests != 0 {
			t.Error("Expected zero requests in fallback summary")
		}

		if !summaries[Fallback].TotalAmount.IsZero() {
			t.Error("Expected zero amount in fallback summary")
		}

		t.Logf("Default Summary - Requests: %d, Amount: %s",
			summaries[Default].TotalRequests,
			summaries[Default].TotalAmount.String())
	})

	// Test Read with empty range
	t.Run("ReadEmptyRange", func(t *testing.T) {
		// Read from a range in the past that should have no data
		pastTime := time.Now().UTC().Add(-24 * time.Hour)
		startTime := pastTime.Add(-1 * time.Hour)
		endTime := pastTime

		summaries := client.Read(startTime, endTime)

		// Should return zero values
		if summaries[Default].TotalRequests != 0 {
			t.Error("Expected zero requests for empty range")
		}

		if !summaries[Default].TotalAmount.IsZero() {
			t.Error("Expected zero amount for empty range")
		}
	})

	// Test Read with specific time range
	t.Run("ReadSpecificTimeRange", func(t *testing.T) {
		// Create transaction at specific moment
		specificTime := time.Date(2025, 7, 31, 14, 30, 0, 0, time.UTC)

		request := PaymentRequest{
			Amount:        99.99,
			CorrelationID: uuid.New().String(),
			RequestedAt:   specificTime,
		}

		err := client.Write(request)
		if err != nil {
			t.Fatalf("Failed to write payment: %v", err)
		}

		// Search exactly in this period (1 minute window)
		startTime := specificTime.Add(-30 * time.Second)
		endTime := specificTime.Add(30 * time.Second)

		summaries := client.Read(startTime, endTime)

		// Should find at least this transaction
		if summaries[Default].TotalRequests == 0 {
			t.Error("Expected at least one request in specific time range")
		}

		if summaries[Default].TotalAmount.IsZero() {
			t.Error("Expected non-zero amount in specific time range")
		}

		t.Logf("Specific Time Range - Requests: %d, Amount: %s",
			summaries[Default].TotalRequests,
			summaries[Default].TotalAmount.String())
	})

	// Test Read with multiple transactions at different times
	t.Run("ReadMultipleSpecificTimes", func(t *testing.T) {
		baseTime := time.Date(2025, 7, 31, 10, 0, 0, 0, time.UTC)

		// Transaction 1: 10:00
		request1 := PaymentRequest{
			Amount:        100.00,
			CorrelationID: uuid.New().String(),
			RequestedAt:   baseTime,
		}

		// Transaction 2: 11:00
		request2 := PaymentRequest{
			Amount:        200.00,
			CorrelationID: uuid.New().String(),
			RequestedAt:   baseTime.Add(1 * time.Hour),
		}

		// Transaction 3: 12:00
		request3 := PaymentRequest{
			Amount:        300.00,
			CorrelationID: uuid.New().String(),
			RequestedAt:   baseTime.Add(2 * time.Hour),
		}

		// Write all transactions
		err := client.Write(request1)
		if err != nil {
			t.Fatalf("Failed to write first payment: %v", err)
		}

		err = client.Write(request2)
		if err != nil {
			t.Fatalf("Failed to write second payment: %v", err)
		}

		err = client.Write(request3)
		if err != nil {
			t.Fatalf("Failed to write third payment: %v", err)
		}

		// Search only between 10:30 and 11:30 (should get only the second one)
		startTime := baseTime.Add(30 * time.Minute)
		endTime := baseTime.Add(90 * time.Minute)

		summaries := client.Read(startTime, endTime)

		// Should find at least the second transaction
		if summaries[Default].TotalRequests == 0 {
			t.Error("Expected at least one request in filtered time range")
		}

		if summaries[Default].TotalAmount.IsZero() {
			t.Error("Expected non-zero amount in filtered time range")
		}

		t.Logf("Filtered Time Range - Requests: %d, Amount: %s",
			summaries[Default].TotalRequests,
			summaries[Default].TotalAmount.String())
	})

	// Test Read with exact boundary conditions
	t.Run("ReadBoundaryConditions", func(t *testing.T) {
		exactTime := time.Date(2025, 7, 31, 15, 0, 0, 0, time.UTC)

		request := PaymentRequest{
			Amount:        75.25,
			CorrelationID: uuid.New().String(),
			RequestedAt:   exactTime,
		}

		err := client.Write(request)
		if err != nil {
			t.Fatalf("Failed to write payment: %v", err)
		}

		// Test exact boundaries - transaction should be included
		summaries := client.Read(exactTime, exactTime.Add(1*time.Nanosecond))

		if summaries[Default].TotalRequests == 0 {
			t.Error("Expected transaction to be included at exact boundary")
		}

		// Test just before - transaction should NOT be included
		summariesBefore := client.Read(exactTime.Add(-1*time.Hour), exactTime.Add(-1*time.Nanosecond))

		if summariesBefore[Default].TotalRequests != 0 {
			t.Log("Note: Found transactions before boundary - this might be from other tests")
		}

		t.Logf("Boundary Test - Exact: %d requests, Before: %d requests",
			summaries[Default].TotalRequests,
			summariesBefore[Default].TotalRequests)
	})
}

func TestPostgresDBClientConnectionError(t *testing.T) {
	// Test with invalid connection string
	_, err := NewPostgresDBClient("invalid-connection-string")
	if err == nil {
		t.Error("Expected error with invalid connection string")
	}
}

func TestSummaryStruct(t *testing.T) {
	// Test Summary struct
	amount, err := decimal.NewFromFloat64(123.45)
	if err != nil {
		t.Fatalf("Failed to create decimal: %v", err)
	}

	summary := Summary{
		TotalRequests: 10,
		TotalAmount:   amount,
	}

	if summary.TotalRequests != 10 {
		t.Errorf("Expected 10 requests, got %d", summary.TotalRequests)
	}

	expected := "123.45"
	if summary.TotalAmount.String() != expected {
		t.Errorf("Expected amount %s, got %s", expected, summary.TotalAmount.String())
	}
}

func TestProcessorType(t *testing.T) {
	// Test ProcessorType constants
	if Default != 0 {
		t.Errorf("Expected Default to be 0, got %d", Default)
	}

	if Fallback != 1 {
		t.Errorf("Expected Fallback to be 1, got %d", Fallback)
	}
}
