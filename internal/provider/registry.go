package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is a concurrency-safe collection of registered LLMProvider
// instances, keyed by their Name(). It is pure infrastructure: it stores and
// retrieves providers and knows nothing about routing, health, or scoring.
//
// The zero value is not usable; construct with NewRegistry. A Registry is safe
// for concurrent use by multiple goroutines.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]LLMProvider
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]LLMProvider),
	}
}

// Register adds p to the registry under p.Name().
//
// It returns an error wrapping:
//   - ErrInvalidRequest if p is nil or its name is empty, and
//   - ErrProviderExists if a provider with the same name is already registered.
//
// Registration is intentionally strict (no silent overwrite) so that
// misconfiguration surfaces immediately at startup rather than as confusing
// runtime behavior.
func (r *Registry) Register(p LLMProvider) error {
	if p == nil {
		return NewError("", "register", fmt.Errorf("%w: provider must not be nil", ErrInvalidRequest))
	}
	name := p.Name()
	if name == "" {
		return NewError("", "register", fmt.Errorf("%w: provider name must not be empty", ErrInvalidRequest))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[name]; exists {
		return NewError(name, "register", ErrProviderExists)
	}
	r.providers[name] = p
	return nil
}

// Get returns the provider registered under name using the comma-ok idiom.
// The boolean is false if no such provider exists.
func (r *Registry) Get(name string) (LLMProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Exists reports whether a provider is registered under name.
func (r *Registry) Exists(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[name]
	return ok
}

// List returns all registered providers, sorted by name for deterministic
// ordering. The returned slice is a copy; mutating it does not affect the
// registry.
func (r *Registry) List() []LLMProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]LLMProvider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Names returns the sorted names of all registered providers.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Len returns the number of registered providers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers)
}
