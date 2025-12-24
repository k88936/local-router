package main

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Test loading valid config
	t.Run("ParseConfig", func(t *testing.T) {
		config, err := loadConfig("../example.config.yaml")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if config.Port != 11435 {
			t.Errorf("Expected port 11435, got %d", config.Port)
		}

		if len(config.Providers) != 5 {
			t.Errorf("Expected 5 providers, got %d", len(config.Providers))
		}

		// Test first provider (aliyun)
		aliyun := config.Providers[0]
		if aliyun.Name != "aliyun" {
			t.Errorf("Expected provider name 'aliyun', got '%s'", aliyun.Name)
		}
		if aliyun.URL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
			t.Errorf("Expected aliyun URL 'https://dashscope.aliyuncs.com/compatible-mode/v1', got '%s'", aliyun.URL)
		}
		if aliyun.Secret != "sk" {
			t.Errorf("Expected aliyun secret 'sk', got '%s'", aliyun.Secret)
		}
		if len(aliyun.Models) != 3 {
			t.Errorf("Expected 3 models for aliyun, got %d", len(aliyun.Models))
		}
		expectedModels := []string{"qwen3-coder-480b-a35b-instruct", "Moonshot-Kimi-K2-Instruct", "qwen3-max"}
		for i, model := range expectedModels {
			if aliyun.Models[i] != model {
				t.Errorf("Expected model %d '%s', got '%s'", i, model, aliyun.Models[i])
			}
		}

		// Test gitcode provider
		gitcode := config.Providers[1]
		if gitcode.Name != "gitcode" {
			t.Errorf("Expected provider name 'gitcode', got '%s'", gitcode.Name)
		}
		if len(gitcode.Models) != 1 {
			t.Errorf("Expected 1 model for gitcode, got %d", len(gitcode.Models))
		}
		if gitcode.Models[0] != "Qwen/Qwen3-Coder-480B-A35B-Instruct" {
			t.Errorf("Expected model 'Qwen/Qwen3-Coder-480B-A35B-Instruct', got '%s'", gitcode.Models[0])
		}
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		_, err := loadConfig("nonexistent.yaml")
		if err == nil {
			t.Error("Expected error for non-existent file, got nil")
		}
	})
}

func TestConfigValidate(t *testing.T) {
	t.Run("ValidConfig", func(t *testing.T) {
		config := &Config{
			Port: 8080,
			Providers: []Provider{
				{
					Name:   "test",
					URL:    "https://example.com",
					Secret: "secret123",
					Models: []string{"model1", "model2"},
				},
			},
		}

		err := config.Validate()
		if err != nil {
			t.Errorf("Expected no error for valid config, got: %v", err)
		}
	})

	t.Run("InvalidPort", func(t *testing.T) {
		config := &Config{
			Port: 70000, // Invalid port
			Providers: []Provider{
				{
					Name:   "test",
					URL:    "https://example.com",
					Secret: "secret123",
					Models: []string{"model1"},
				},
			},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for invalid port, got nil")
		}
		if err.Error() != "port must be between 1 and 65535" {
			t.Errorf("Expected specific port error, got: %v", err)
		}
	})

	t.Run("NoProviders", func(t *testing.T) {
		config := &Config{
			Port:      8080,
			Providers: []Provider{},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for no providers, got nil")
		}
		if err.Error() != "at least one provider must be configured" {
			t.Errorf("Expected specific providers error, got: %v", err)
		}
	})

	t.Run("EmptyProviderName", func(t *testing.T) {
		config := &Config{
			Port: 8080,
			Providers: []Provider{
				{
					Name:   "", // Empty name
					URL:    "https://example.com",
					Secret: "secret123",
					Models: []string{"model1"},
				},
			},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for empty provider name, got nil")
		}
	})

	t.Run("InvalidURL", func(t *testing.T) {
		config := &Config{
			Port: 8080,
			Providers: []Provider{
				{
					Name:   "test",
					URL:    "http://invalid url with spaces", // Invalid URL with spaces
					Secret: "secret123",
					Models: []string{"model1"},
				},
			},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for invalid URL, got nil")
		}
	})

	t.Run("EmptySecret", func(t *testing.T) {
		config := &Config{
			Port: 8080,
			Providers: []Provider{
				{
					Name:   "test",
					URL:    "https://example.com",
					Secret: "", // Empty secret
					Models: []string{"model1"},
				},
			},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for empty secret, got nil")
		}
	})

	t.Run("NoModels", func(t *testing.T) {
		config := &Config{
			Port: 8080,
			Providers: []Provider{
				{
					Name:   "test",
					URL:    "https://example.com",
					Secret: "secret123",
					Models: []string{}, // No models
				},
			},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for no models, got nil")
		}
	})

	t.Run("EmptyModelName", func(t *testing.T) {
		config := &Config{
			Port: 8080,
			Providers: []Provider{
				{
					Name:   "test",
					URL:    "https://example.com",
					Secret: "secret123",
					Models: []string{"", "model2"}, // Empty model name
				},
			},
		}

		err := config.Validate()
		if err == nil {
			t.Error("Expected error for empty model name, got nil")
		}
	})
}
