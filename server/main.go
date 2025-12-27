package main

import (
	"log"
	"os"
	"strings"
)

func findConfigFile() string {
	configPaths := []string{
		".local-router.yaml",
		os.Getenv("HOME") + "/.local-router.yaml",
		os.Getenv("USERPROFILE") + "/.local-router.yaml",
	}

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	panic("Failed to find example.config.yaml in HOME, USERPROFILE, or current directory")
}

func parseLogLevel(levelStr string) LogLevel {
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

func main() {
	configPath := findConfigFile()
	config, err := loadConfig(configPath)
	if err != nil {
		log.Panicf("Failed to load config from %s: %v", configPath, err)
	}

	if err := config.Validate(); err != nil {
		log.Panicf("Config validation failed: %v", err)
	}

	// Initialize logger with configured level
	logLevel := parseLogLevel(config.LogLevel)
	InitLogger(logLevel)
	logger := GetLogger()

	logger.Info("Loaded configuration for %d providers", len(config.Providers))
	for i, provider := range config.Providers {
		logger.Info("Provider %d: %s with %d models", i+1, provider.Name, len(provider.Models))
	}

	server := NewServer(config, configPath)
	if err := server.Start(); err != nil {
		logger.Panic("Failed to start server: %v", err)
	}
}
