package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Compile-time assertions.
var (
	_ Cache         = (*RedisCache)(nil)
	_ StatsReporter = (*RedisCache)(nil)
)

// RedisClient is the subset of the go-redis client the L2 cache uses. Depending
// on this interface (rather than *redis.Client) keeps the cache testable and lets
// callers pass a client, cluster client, or a fake.
type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	Ping(ctx context.Context) *redis.StatusCmd
}

// NewRedisClient builds a go-redis client from configuration. The caller owns the
// client's lifecycle (Close); the RedisCache does not close an injected client.
func NewRedisClient(cfg RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

// redisEnvelope is the stored value: the payload plus timestamps, so Get can
// return full Entry metadata (used to preserve TTL when promoting to faster
// levels). Redis's own EX handles eviction; the envelope carries metadata.
type redisEnvelope struct {
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Value     []byte    `json:"value"`
}

// RedisCache is the L2 exact-match cache backed by Redis. It is fleet-shared: an
// entry written by one instance is visible to all.
type RedisCache struct {
	client     RedisClient
	prefix     string
	defaultTTL time.Duration
	clock      func() time.Time
	stats      *Stats
}

// RedisOption configures a RedisCache.
type RedisOption func(*RedisCache)

// WithRedisClock injects a time source for deterministic metadata in tests.
func WithRedisClock(now func() time.Time) RedisOption {
	return func(c *RedisCache) {
		if now != nil {
			c.clock = now
		}
	}
}

// NewRedisCache constructs the L2 cache over the given client and config.
func NewRedisCache(client RedisClient, cfg RedisConfig, opts ...RedisOption) *RedisCache {
	cfg = cfg.withDefaults()
	c := &RedisCache{
		client:     client,
		prefix:     cfg.Prefix,
		defaultTTL: cfg.DefaultTTL,
		clock:      time.Now,
		stats:      NewStats(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Name returns the level identifier ("l2").
func (c *RedisCache) Name() string { return LevelL2 }

func (c *RedisCache) redisKey(key string) string { return c.prefix + key }

// Get returns the entry for key. A redis.Nil (missing key) is a miss, not an
// error. Any other Redis error is returned so the Manager can treat this level as
// unavailable (fail-safe).
func (c *RedisCache) Get(ctx context.Context, key string) (Entry, bool, error) {
	raw, err := c.client.Get(ctx, c.redisKey(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		c.stats.Miss()
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}

	var env redisEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// Corrupt value: drop it and report a miss.
		_ = c.client.Del(ctx, c.redisKey(key)).Err()
		c.stats.Miss()
		return Entry{}, false, nil
	}
	entry := Entry{Key: key, Value: env.Value, CreatedAt: env.CreatedAt, ExpiresAt: env.ExpiresAt}
	if entry.Expired(c.clock()) {
		_ = c.client.Del(ctx, c.redisKey(key)).Err()
		c.stats.Miss()
		return Entry{}, false, nil
	}
	c.stats.Hit()
	return entry, true, nil
}

// Set stores value under key with the given TTL (or the default when ttl <= 0),
// using Redis's native expiration.
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	now := c.clock()
	effectiveTTL, expiresAt := resolveExpiry(now, ttl, c.defaultTTL)
	payload, err := json.Marshal(redisEnvelope{CreatedAt: now, ExpiresAt: expiresAt, Value: value})
	if err != nil {
		return err
	}
	// A non-positive expiration means "no expiry" to go-redis.
	redisTTL := effectiveTTL
	if redisTTL < 0 {
		redisTTL = 0
	}
	if err := c.client.Set(ctx, c.redisKey(key), payload, redisTTL).Err(); err != nil {
		return err
	}
	c.stats.Set()
	return nil
}

// Delete removes key.
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	if err := c.client.Del(ctx, c.redisKey(key)).Err(); err != nil {
		return err
	}
	c.stats.Delete()
	return nil
}

// Exists reports whether key is present (and unexpired, per Redis).
func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, c.redisKey(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Clear removes only this cache's namespaced keys, scanning by prefix so it never
// flushes unrelated data sharing the Redis instance.
func (c *RedisCache) Clear(ctx context.Context) error {
	var cursor uint64
	for {
		keys, next, err := c.client.Scan(ctx, cursor, c.prefix+"*", 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// Ping verifies connectivity to Redis. Later phases (health monitoring) can use it.
func (c *RedisCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Stats returns the level's statistics snapshot.
func (c *RedisCache) Stats() StatsSnapshot { return c.stats.Snapshot() }
