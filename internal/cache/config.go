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

	DefaultRedisPrefix       = "modelmesh:cache:"
	DefaultSemanticThreshold = 0.92
	DefaultSemanticTopK      = 5
	DefaultEmbeddingDims     = 128
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

// RedisConfig configures the L2 Redis cache. Enabled is opt-in because it needs
// an external Redis; L1 works without it.
type RedisConfig struct {
	Enabled    bool          `json:"enabled"`
	Address    string        `json:"address"`
	Password   string        `json:"password,omitempty"`
	DB         int           `json:"db"`
	Prefix     string        `json:"prefix"`
	DefaultTTL time.Duration `json:"default_ttl"`
}

func (c RedisConfig) withDefaults() RedisConfig {
	if c.Prefix == "" {
		c.Prefix = DefaultRedisPrefix
	}
	return c
}

// SemanticConfig configures the L3 semantic cache. Enabled is opt-in because it
// needs an embedder.
type SemanticConfig struct {
	Enabled bool `json:"enabled"`
	// Threshold is the minimum cosine similarity (0..1) for a semantic hit.
	Threshold float64 `json:"threshold"`
	// TopK is the number of nearest neighbors examined per lookup.
	TopK int `json:"top_k"`
	// EmbeddingDims is the dimensionality of the default hashing embedder.
	EmbeddingDims int           `json:"embedding_dims"`
	DefaultTTL    time.Duration `json:"default_ttl"`
}

func (c SemanticConfig) withDefaults() SemanticConfig {
	if c.Threshold == 0 {
		c.Threshold = DefaultSemanticThreshold
	}
	if c.TopK == 0 {
		c.TopK = DefaultSemanticTopK
	}
	if c.EmbeddingDims == 0 {
		c.EmbeddingDims = DefaultEmbeddingDims
	}
	return c
}

// WritePolicy controls how the Manager populates the cache on a store. The zero
// value is write-through to every level, synchronously.
type WritePolicy struct {
	// DisabledLevels lists level names to skip when populating (e.g. []{"l3"} to
	// avoid semantic writes while still reading from L3). Empty writes all levels.
	DisabledLevels []string `json:"disabled_levels,omitempty"`
	// Async, when true, populates the cache in the background so the request path
	// is not blocked by cache writes. In-flight writes are drained on Close.
	Async bool `json:"async"`
}

// writes reports whether the policy permits writing to the named level.
func (p WritePolicy) writes(level string) bool {
	for _, d := range p.DisabledLevels {
		if d == level {
			return false
		}
	}
	return true
}

// Config configures the cache subsystem.
type Config struct {
	// Enabled turns caching on or off. When false, the gateway skips the cache.
	Enabled bool `json:"enabled"`
	// DefaultTTL is the default entry lifetime across levels.
	DefaultTTL time.Duration `json:"default_ttl"`
	// Memory configures the L1 level.
	Memory MemoryConfig `json:"memory"`
	// Redis configures the optional L2 level.
	Redis RedisConfig `json:"redis"`
	// Semantic configures the optional L3 level.
	Semantic SemanticConfig `json:"semantic"`
	// Write is the cache write policy (which levels to populate, sync vs async).
	Write WritePolicy `json:"write"`
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
		Redis:    RedisConfig{Prefix: DefaultRedisPrefix},
		Semantic: SemanticConfig{Threshold: DefaultSemanticThreshold, TopK: DefaultSemanticTopK, EmbeddingDims: DefaultEmbeddingDims},
	}
}

// WithDefaults returns a copy of c with zero-valued fields filled in. Level
// DefaultTTLs inherit the top-level DefaultTTL when unset.
func (c Config) WithDefaults() Config {
	if c.DefaultTTL == 0 {
		c.DefaultTTL = DefaultTTL
	}
	if c.Memory.DefaultTTL == 0 {
		c.Memory.DefaultTTL = c.DefaultTTL
	}
	c.Redis = c.Redis.withDefaults()
	if c.Redis.DefaultTTL == 0 {
		c.Redis.DefaultTTL = c.DefaultTTL
	}
	c.Semantic = c.Semantic.withDefaults()
	if c.Semantic.DefaultTTL == 0 {
		c.Semantic.DefaultTTL = c.DefaultTTL
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
	if c.Memory.DefaultTTL < 0 || c.Memory.CleanupInterval < 0 {
		return fmt.Errorf("%w: memory durations must not be negative", ErrInvalidCacheConfig)
	}
	if c.Redis.Enabled {
		if c.Redis.Address == "" {
			return fmt.Errorf("%w: redis.address is required when redis is enabled", ErrInvalidCacheConfig)
		}
		if c.Redis.DB < 0 || c.Redis.DefaultTTL < 0 {
			return fmt.Errorf("%w: redis.db and redis.default_ttl must not be negative", ErrInvalidCacheConfig)
		}
	}
	if c.Semantic.Enabled {
		if c.Semantic.Threshold < 0 || c.Semantic.Threshold > 1 {
			return fmt.Errorf("%w: semantic.threshold must be within [0,1]", ErrInvalidCacheConfig)
		}
		if c.Semantic.TopK < 1 {
			return fmt.Errorf("%w: semantic.top_k must be at least 1", ErrInvalidCacheConfig)
		}
		if c.Semantic.EmbeddingDims < 1 || c.Semantic.DefaultTTL < 0 {
			return fmt.Errorf("%w: semantic.embedding_dims must be positive and default_ttl non-negative", ErrInvalidCacheConfig)
		}
	}
	return nil
}
