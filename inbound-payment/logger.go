package main

import (
	"os"

	"go.uber.org/zap"
)

// LoggerConfig holds the configuration for the logger
type LoggerConfig struct {
	Level       string   `json:"level"`
	Environment string   `json:"environment"`
	OutputPaths []string `json:"outputPaths"`
}

// NewLoggerConfig creates a new logger configuration based on environment
func NewLoggerConfig() LoggerConfig {
	env := getEnv("ENV", "production")
	level := getEnv("LOG_LEVEL", "info")

	return LoggerConfig{
		Level:       level,
		Environment: env,
		OutputPaths: []string{"stdout"},
	}
}

// InitLogger initializes and returns a zap logger based on the configuration
func InitLogger(config LoggerConfig) (*zap.Logger, error) {
	var zapConfig zap.Config

	if config.Environment == "development" || config.Environment == "dev" {
		zapConfig = zap.NewDevelopmentConfig()
	} else {
		zapConfig = zap.NewProductionConfig()
	}

	// Set log level
	level, err := zap.ParseAtomicLevel(config.Level)
	if err != nil {
		return nil, err
	}
	zapConfig.Level = level

	// Set output paths
	zapConfig.OutputPaths = config.OutputPaths
	zapConfig.ErrorOutputPaths = config.OutputPaths

	return zapConfig.Build()
}

// Helper function to get environment variables with default values
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
