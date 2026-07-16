package shadow

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	mathrand "math/rand"
	"sync"
)

// defaultSampler returns a concurrency-safe [0,1) sampler seeded from a
// crypto-random seed. It is used when no sampler is injected.
func defaultSampler() func() float64 {
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err != nil {
		// Fall back to a fixed seed; sampling randomness is non-critical.
		binary.LittleEndian.PutUint64(seed[:], 0x9e3779b97f4a7c15)
	}
	r := mathrand.New(mathrand.NewSource(int64(binary.LittleEndian.Uint64(seed[:]))))
	var mu sync.Mutex
	return func() float64 {
		mu.Lock()
		defer mu.Unlock()
		return r.Float64()
	}
}

// newID returns a unique shadow execution identifier.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "shadow_0000000000000000"
	}
	return "shadow_" + hex.EncodeToString(b[:])
}
