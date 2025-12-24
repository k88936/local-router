package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"

	"gopkg.in/yaml.v3"
)

func (c *Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}

	if len(c.Providers) == 0 {
		return errors.New("at least one provider must be configured")
	}

	for i, provider := range c.Providers {
		if provider.Name == "" {
			return fmt.Errorf("provider %d: name cannot be empty", i+1)
		}
		if provider.URL == "" {
			return fmt.Errorf("provider %s: URL cannot be empty", provider.Name)
		}
		if _, err := url.Parse(provider.URL); err != nil {
			return fmt.Errorf("provider %s: invalid URL: %w", provider.Name, err)
		}
		if provider.Secret == "" {
			return fmt.Errorf("provider %s: secret cannot be empty", provider.Name)
		}
		if len(provider.Models) == 0 {
			return fmt.Errorf("provider %s: at least one model must be specified", provider.Name)
		}
		for j, model := range provider.Models {
			if model == "" {
				return fmt.Errorf("provider %s: model %d cannot be empty", provider.Name, j+1)
			}
		}
	}

	return nil
}

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}
