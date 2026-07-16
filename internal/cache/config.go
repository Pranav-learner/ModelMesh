package cache

import (
	"fmt"
	"time"
)

// Default cache parameters, named to keep logic free of magic numbers.
const (
	DefaultTTL             = 5 * time.Minute
	DefaultMaxEntries      = 10_000
	DefaultCleanupInterval = 1 * time.Minute
)

// MemoryConfig configures the L1 in-memory cache.
type MemoryConfig struct {
	// MaxEntries bounds the number of stored items; the least-recently-used entry
	// is evicted when the bound is exceeded. Zero means unbounded.
	MaxEntries int `json:"max_entries"`
	// DefaultTTL is the TTL applied to sets that do not specify one. Zero inherits
	// the top-level Config.DefaultTTL.
	DefaultTTL time.Duration `json:"default_ttl"`
	// CleanupInterval is how often the background janitor sweeps expired entries.
	// Zero disables the janitor (entries still expire lazily on access).
	CleanupInterval time.Duration `json:"cleanup_interval"`
}

// Config configures the cache subsystem.
type Config struct {
	// Enabled turns caching on or off. When false, the gateway skips the cache.
	Enabled bool `json:"enabled"`
	// DefaultTTL is the default entry lifetime across levels.
	DefaultTTL time.Duration `json:"default_ttl"`
	// Memory configures the L1 level.
	Memory MemoryConfig `json:"memory"`
}

// DefaultConfig returns an enabled cache configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:    true,
		DefaultTTL: DefaultTTL,
		Memory: MemoryConfig{
			MaxEntries:      DefaultMaxEntries,
			CleanupInterval: DefaultCleanupInterval,
		},
	}
}

// WithDefaults returns a copy of c with zero-valued fields filled in. The L1
// DefaultTTL inherits the top-level DefaultTTL when unset.
func (c Config) WithDefaults() Config {
	if c.DefaultTTL == 0 {
		c.DefaultTTL = DefaultTTL
	}
	if c.Memory.DefaultTTL == 0 {
		c.Memory.DefaultTTL = c.DefaultTTL
	}
	return c
}

// Validate checks the cache configuration. Errors wrap ErrInvalidCacheConfig.
func (c Config) Validate() error {
	if c.DefaultTTL < 0 {
		return fmt.Errorf("%w: default_ttl must not be negative", ErrInvalidCacheConfig)
	}
	if c.Memory.MaxEntries < 0 {
		return fmt.Errorf("%w: memory.max_entries must not be negative", ErrInvalidCacheConfig)
	}
	if c.Memory.DefaultTTL < 0 {
		return fmt.Errorf("%w: memory.default_ttl must not be negative", ErrInvalidCacheConfig)
	}
	if c.Memory.CleanupInterval < 0 {
		return fmt.Errorf("%w: memory.cleanup_interval must not be negative", ErrInvalidCacheConfig)
	}
	return nil
}
