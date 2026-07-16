// Command cachedemo demonstrates the completed Multi-Level Cache end to end,
// fully offline, using mock providers and an in-process Redis (miniredis).
//
// It walks the canonical scenario:
//
//	Q1 (new)        -> provider miss  -> stored in L1 + L2 + L3
//	Q2 (identical)  -> L1 memory hit
//	Q3 (L1 evicted) -> L2 Redis hit   -> promoted back into L1
//	Q4 (paraphrased)-> L3 semantic hit (cosine similarity)
//
// For each it prints the cache layer used, latency, whether it was cached, and the
// semantic similarity, then the aggregate cache analytics and savings.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// slowChat simulates a provider whose completion takes ~40ms (so cache hits are
// visibly faster). Latency is on Chat only, not model discovery.
func slowChat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	time.Sleep(40 * time.Millisecond)
	content := "A semantic cache reuses a prior response for a semantically similar prompt."
	inTokens := 0
	for _, m := range req.Messages {
		inTokens += len(strings.Fields(m.Content))
	}
	outTokens := len(strings.Fields(content))
	return provider.ChatResponse{
		ID:       "demo",
		Provider: "openai",
		Model:    "mock-chat",
		Choices: []provider.Choice{{
			Message:      provider.ChatMessage{Role: provider.RoleAssistant, Content: content},
			FinishReason: provider.FinishReasonStop,
		}},
		Usage: provider.Usage{PromptTokens: inTokens, CompletionTokens: outTokens, TotalTokens: inTokens + outTokens},
	}, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// --- provider + router (a mock provider with simulated latency) ---
	reg := provider.NewRegistry()
	_ = reg.Register(mock.New(mock.WithName("openai"), mock.WithChatFunc(slowChat)))
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		return err
	}

	// --- three cache levels: L1 memory, L2 Redis (miniredis), L3 semantic ---
	l1 := cache.NewMemoryCache(cache.MemoryConfig{DefaultTTL: time.Minute})
	mr, err := miniredis.Run()
	if err != nil {
		return err
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	l2 := cache.NewRedisCache(rdb, cache.RedisConfig{Prefix: "demo:", DefaultTTL: time.Minute})
	// Hashing embedder + in-memory vector store; a modest threshold so a one-word
	// paraphrase clears it.
	l3 := cache.NewSemanticCache(cache.SemanticConfig{Threshold: 0.8, TopK: 5, EmbeddingDims: 128, DefaultTTL: time.Minute}, nil, nil)
	cm := cache.NewManager([]cache.Cache{l1, l2}, cache.WithSemantic(l3))
	defer func() { _ = cm.Close() }()

	// --- gateway with a per-token cost estimator ---
	cost := func(_ string, u provider.Usage) float64 { return float64(u.TotalTokens) * 0.00003 }
	gw := gateway.New(router, cm, cache.DefaultConfig(), gateway.WithCostEstimator(cost))

	ask := func(label, content string) {
		start := time.Now()
		res, err := gw.Chat(ctx, chatReq(content))
		if err != nil {
			fmt.Printf("  %-26s ERROR: %v\n", label, err)
			return
		}
		layer := "PROVIDER (miss)"
		if res.Cached {
			layer = cache.LayerUsed(cache.Entry{Level: res.CacheLevel})
		}
		sim := ""
		if res.Similarity > 0 {
			sim = fmt.Sprintf(" similarity=%.3f", res.Similarity)
		}
		fmt.Printf("  %-26s layer=%-14s latency=%-10s cached=%v%s\n",
			label, layer, time.Since(start).Round(time.Microsecond), res.Cached, sim)
	}

	fmt.Println("=== Multi-Level Cache demo ===")
	ask("Q1 (new question)", "how does a semantic cache work")
	ask("Q2 (identical)", "how does a semantic cache work")

	_ = l1.Clear(ctx) // evict L1 so the next lookup falls through to Redis
	ask("Q3 (L1 evicted)", "how does a semantic cache work")

	ask("Q4 (paraphrased)", "how does the semantic cache work")

	// --- analytics ---
	s := cm.Stats()
	g := gw.Stats()
	fmt.Println("\n=== Cache analytics ===")
	fmt.Printf("  hit ratio          : %.2f%%\n", s.HitRatio*100)
	fmt.Printf("  memory / redis / semantic hits: %d / %d / %d\n", s.MemoryHits, s.RedisHits, s.SemanticHits)
	fmt.Printf("  misses             : %d\n", s.Misses)
	fmt.Printf("  avg lookup time    : %s\n", s.AverageLookupTime.Round(time.Nanosecond))
	fmt.Printf("  avg similarity     : %.4f\n", s.AverageSimilarity)
	fmt.Printf("  tokens saved       : %d\n", g.TokensSaved)
	fmt.Printf("  estimated cost saved: $%.6f\n", g.CostSavedUSD)
	return nil
}

func chatReq(content string) provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: content}}}
}
