package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fullStack builds a Manager with all three levels: L1 memory, L2 Redis
// (miniredis), and L3 semantic (mock embedder), plus references to each level.
func fullStack(t *testing.T, opts ...ManagerOption) (*Manager, *MemoryCache, *RedisCache, *SemanticCacheImpl) {
	t.Helper()

	l1 := NewMemoryCache(MemoryConfig{DefaultTTL: time.Minute})

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	l2 := NewRedisCache(client, RedisConfig{Prefix: "it:", DefaultTTL: time.Minute})

	emb := mockEmbedder{dims: 2, vectors: map[string][]float32{
		"stored":  {1, 0},
		"similar": {0.99, 0.14}, // cosine ~0.99
		"distant": {0, 1},       // cosine 0
	}}
	l3 := NewSemanticCache(SemanticConfig{Threshold: 0.9, TopK: 5, EmbeddingDims: 2, DefaultTTL: time.Minute}, emb, nil)

	all := append([]ManagerOption{WithSemantic(l3)}, opts...)
	m := NewManager([]Cache{l1, l2}, all...)
	t.Cleanup(func() { _ = m.Close() })
	return m, l1, l2, l3
}

func q(key, text string) Query { return Query{Key: key, Model: "gpt", Text: text} }

func TestIntegration_WriteThrough(t *testing.T) {
	m, l1, l2, _ := fullStack(t)
	ctx := context.Background()

	if err := m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute); err != nil {
		t.Fatalf("Store() = %v", err)
	}
	if ok, _ := l1.Exists(ctx, "k1"); !ok {
		t.Errorf("write-through missed L1")
	}
	if ok, _ := l2.Exists(ctx, "k1"); !ok {
		t.Errorf("write-through missed L2")
	}
	// L3 was written under the text; a semantic lookup finds it.
	if _, found, _ := m.semantic.Lookup(ctx, "similar", "gpt"); !found {
		t.Errorf("write-through missed L3 (semantic)")
	}
}

func TestIntegration_L1Hit(t *testing.T) {
	m, _, _, _ := fullStack(t)
	ctx := context.Background()
	_ = m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute)

	e, found, _ := m.Lookup(ctx, q("k1", "stored"))
	if !found || e.Level != LevelL1 {
		t.Fatalf("expected L1 hit, got found=%v level=%q", found, e.Level)
	}
}

func TestIntegration_L2HitAndPromotion(t *testing.T) {
	m, l1, _, _ := fullStack(t)
	ctx := context.Background()
	_ = m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute)

	// Evict L1 so the exact key only survives in L2.
	_ = l1.Clear(ctx)

	e, found, _ := m.Lookup(ctx, q("k1", "stored"))
	if !found || e.Level != LevelL2 {
		t.Fatalf("expected L2 hit, got found=%v level=%q", found, e.Level)
	}
	// Promotion: L1 now holds it again.
	if ok, _ := l1.Exists(ctx, "k1"); !ok {
		t.Errorf("L2 hit was not promoted into L1")
	}
}

func TestIntegration_L3HitAndPromotion(t *testing.T) {
	m, l1, l2, _ := fullStack(t)
	ctx := context.Background()
	_ = m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute)

	// Evict exact levels so only the semantic index can serve it.
	_ = l1.Clear(ctx)
	_ = l2.Clear(ctx)

	// A different exact key but a semantically-similar text -> L3 hit.
	e, found, _ := m.Lookup(ctx, q("k2", "similar"))
	if !found || e.Level != LevelL3 {
		t.Fatalf("expected L3 hit, got found=%v level=%q", found, e.Level)
	}
	if e.Similarity < 0.9 {
		t.Errorf("semantic hit similarity = %v, want >= 0.9", e.Similarity)
	}
	// Promotion into both exact levels under the new key.
	if ok, _ := l1.Exists(ctx, "k2"); !ok {
		t.Errorf("L3 hit not promoted into L1")
	}
	if ok, _ := l2.Exists(ctx, "k2"); !ok {
		t.Errorf("L3 hit not promoted into L2")
	}
}

func TestIntegration_Miss(t *testing.T) {
	m, _, _, _ := fullStack(t)
	if _, found, err := m.Lookup(context.Background(), q("nope", "distant")); found || err != nil {
		t.Errorf("Lookup(miss) = found=%v err=%v", found, err)
	}
}

func TestIntegration_Expiration(t *testing.T) {
	// Expiration through the Manager on L1 with a shared injected clock.
	clk := newClock()
	l1 := NewMemoryCache(MemoryConfig{DefaultTTL: time.Minute}, WithClock(clk.Now))
	t.Cleanup(func() { _ = l1.Close() })
	m := NewManager([]Cache{l1})

	ctx := context.Background()
	_ = m.Store(ctx, q("k", ""), []byte("v"), time.Minute)
	if _, found, _ := m.Lookup(ctx, q("k", "")); !found {
		t.Fatalf("entry missing before expiry")
	}
	clk.Advance(61 * time.Second)
	if _, found, _ := m.Lookup(ctx, q("k", "")); found {
		t.Errorf("entry served after expiry")
	}
}

func TestIntegration_Analytics(t *testing.T) {
	m, l1, l2, _ := fullStack(t)
	ctx := context.Background()
	_ = m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute)

	_, _, _ = m.Lookup(ctx, q("k1", "stored")) // L1 hit
	_ = l1.Clear(ctx)
	_, _, _ = m.Lookup(ctx, q("k1", "stored")) // L2 hit (promotes to L1)
	_ = l1.Clear(ctx)
	_ = l2.Clear(ctx)
	_, _, _ = m.Lookup(ctx, q("k2", "similar")) // L3 semantic hit
	_, _, _ = m.Lookup(ctx, q("k3", "distant")) // miss

	s := m.Stats()
	if s.MemoryHits < 1 || s.RedisHits < 1 || s.SemanticHits < 1 {
		t.Errorf("per-source hits = mem:%d redis:%d sem:%d", s.MemoryHits, s.RedisHits, s.SemanticHits)
	}
	if s.Misses != 1 {
		t.Errorf("Misses = %d, want 1", s.Misses)
	}
	if s.HitRatio <= 0 || s.HitRatio >= 1 {
		t.Errorf("HitRatio = %v, want in (0,1)", s.HitRatio)
	}
	if s.SemanticHitRate <= 0 {
		t.Errorf("SemanticHitRate = %v, want > 0", s.SemanticHitRate)
	}
	if s.AverageSimilarity < 0.9 {
		t.Errorf("AverageSimilarity = %v, want >= 0.9", s.AverageSimilarity)
	}
	if s.AverageLookupTime <= 0 {
		t.Errorf("AverageLookupTime = %v, want > 0", s.AverageLookupTime)
	}
}

func TestIntegration_WritePolicy_DisabledLevel(t *testing.T) {
	m, l1, l2, _ := fullStack(t, WithWritePolicy(WritePolicy{DisabledLevels: []string{LevelL2}}))
	ctx := context.Background()
	_ = m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute)

	if ok, _ := l1.Exists(ctx, "k1"); !ok {
		t.Errorf("L1 should still be written")
	}
	if ok, _ := l2.Exists(ctx, "k1"); ok {
		t.Errorf("L2 write should be disabled by policy")
	}
}

func TestIntegration_WritePolicy_Async(t *testing.T) {
	m, l1, _, _ := fullStack(t, WithWritePolicy(WritePolicy{Async: true}))
	ctx := context.Background()
	_ = m.Store(ctx, q("k1", "stored"), []byte("v"), time.Minute)

	// Async population completes shortly; poll rather than assume immediate.
	deadline := time.Now().Add(time.Second)
	for {
		if ok, _ := l1.Exists(ctx, "k1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("async write did not populate L1")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIntegration_ConcurrentAccess(t *testing.T) {
	m, _, _, _ := fullStack(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k" + itoa(i%8)
			_ = m.Store(ctx, q(key, "stored"), []byte("v"), time.Minute)
			_, _, _ = m.Lookup(ctx, q(key, "stored"))
			_ = m.Stats()
		}(i)
	}
	wg.Wait()
}
