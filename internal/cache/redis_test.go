package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := NewRedisCache(client, RedisConfig{Prefix: "mm:test:", DefaultTTL: time.Minute})
	return c, mr
}

func TestRedis_SetGet(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	e, found, err := c.Get(ctx, "k")
	if err != nil || !found || string(e.Value) != "v" {
		t.Fatalf("Get = %q,%v,%v", e.Value, found, err)
	}
	if e.CreatedAt.IsZero() {
		t.Errorf("entry missing CreatedAt metadata")
	}
}

func TestRedis_Miss(t *testing.T) {
	c, _ := newRedisCache(t)
	if _, found, err := c.Get(context.Background(), "nope"); found || err != nil {
		t.Errorf("Get(missing) = found=%v err=%v", found, err)
	}
}

func TestRedis_Delete(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), 0)
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, found, _ := c.Get(ctx, "k"); found {
		t.Errorf("entry present after Delete")
	}
}

func TestRedis_Exists(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), 0)
	if ok, _ := c.Exists(ctx, "k"); !ok {
		t.Errorf("Exists(k) = false")
	}
	if ok, _ := c.Exists(ctx, "x"); ok {
		t.Errorf("Exists(x) = true")
	}
}

func TestRedis_TTLExpiry(t *testing.T) {
	c, mr := newRedisCache(t)
	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), 30*time.Second)

	if _, found, _ := c.Get(ctx, "k"); !found {
		t.Fatalf("entry missing before expiry")
	}
	mr.FastForward(31 * time.Second) // advance Redis's clock past the TTL
	if _, found, _ := c.Get(ctx, "k"); found {
		t.Errorf("entry present after TTL expiry")
	}
}

func TestRedis_ClearNamespaced(t *testing.T) {
	c, mr := newRedisCache(t)
	ctx := context.Background()
	_ = c.Set(ctx, "a", []byte("1"), 0)
	_ = c.Set(ctx, "b", []byte("2"), 0)
	// A key outside our namespace must survive Clear.
	if err := mr.Set("other:key", "keep"); err != nil {
		t.Fatalf("seed unrelated key: %v", err)
	}

	if err := c.Clear(ctx); err != nil {
		t.Fatalf("Clear() = %v", err)
	}
	if _, found, _ := c.Get(ctx, "a"); found {
		t.Errorf("namespaced key survived Clear")
	}
	if !mr.Exists("other:key") {
		t.Errorf("Clear removed an unrelated key")
	}
}

func TestRedis_CorruptValueIsMiss(t *testing.T) {
	c, mr := newRedisCache(t)
	ctx := context.Background()
	// Write a non-JSON value directly under our prefix.
	if err := mr.Set("mm:test:k", "not-json"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, found, err := c.Get(ctx, "k"); found || err != nil {
		t.Errorf("corrupt value = found=%v err=%v, want miss", found, err)
	}
}

func TestRedis_PingAndStats(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping() = %v", err)
	}
	_ = c.Set(ctx, "k", []byte("v"), 0)
	_, _, _ = c.Get(ctx, "k")    // hit
	_, _, _ = c.Get(ctx, "miss") // miss
	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 || s.Sets != 1 {
		t.Errorf("redis stats = %+v", s)
	}
	if c.Name() != LevelL2 {
		t.Errorf("Name = %q, want l2", c.Name())
	}
}
