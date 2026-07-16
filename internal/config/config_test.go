package config

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultConfig_IsValid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v, want nil", err)
	}
}

func TestConfig_WithDefaults(t *testing.T) {
	c := Config{}.WithDefaults()

	if c.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %s, want %s", c.RequestTimeout, DefaultRequestTimeout)
	}
	if c.HealthCheckInterval != DefaultHealthCheckInterval {
		t.Errorf("HealthCheckInterval = %s, want %s", c.HealthCheckInterval, DefaultHealthCheckInterval)
	}

	// A supplied value must be preserved.
	custom := Config{RequestTimeout: 5 * time.Second}.WithDefaults()
	if custom.RequestTimeout != 5*time.Second {
		t.Errorf("WithDefaults overwrote a supplied RequestTimeout")
	}
}

func TestConfig_Validate(t *testing.T) {
	base := DefaultConfig()

	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr bool
	}{
		{"default is valid", func(*Config) {}, false},
		{"zero request timeout", func(c *Config) { c.RequestTimeout = 0 }, true},
		{"negative request timeout", func(c *Config) { c.RequestTimeout = -1 }, true},
		{"negative retry count", func(c *Config) { c.RetryCount = -1 }, true},
		{"zero retry count is allowed", func(c *Config) { c.RetryCount = 0 }, false},
		{"zero health interval", func(c *Config) { c.HealthCheckInterval = 0 }, true},
		{
			name: "valid providers",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{Name: "openai", Enabled: true}}
			},
			wantErr: false,
		},
		{
			name:    "provider with empty name",
			mutate:  func(c *Config) { c.Providers = []ProviderConfig{{Name: ""}} },
			wantErr: true,
		},
		{
			name: "duplicate provider names",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{Name: "openai"}, {Name: "openai"}}
			},
			wantErr: true,
		},
		{
			name: "negative provider timeout",
			mutate: func(c *Config) {
				c.Providers = []ProviderConfig{{Name: "openai", Timeout: -1}}
			},
			wantErr: true,
		},
		{
			name: "default provider not in list",
			mutate: func(c *Config) {
				c.DefaultProvider = "ghost"
				c.Providers = []ProviderConfig{{Name: "openai"}}
			},
			wantErr: true,
		},
		{
			name: "default provider in list",
			mutate: func(c *Config) {
				c.DefaultProvider = "openai"
				c.Providers = []ProviderConfig{{Name: "openai"}}
			},
			wantErr: false,
		},
		{
			name:    "default provider without any provider list is allowed",
			mutate:  func(c *Config) { c.DefaultProvider = "openai" },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error")
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Errorf("error does not wrap ErrInvalidConfig: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
