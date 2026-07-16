package provider_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := provider.NewRegistry()
	p := mock.New(mock.WithName("alpha"))

	if err := reg.Register(p); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	got, ok := reg.Get("alpha")
	if !ok {
		t.Fatalf("Get(alpha) not found after Register")
	}
	if got.Name() != "alpha" {
		t.Errorf("Get(alpha).Name() = %q, want %q", got.Name(), "alpha")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(mock.New(mock.WithName("dup")))

	err := reg.Register(mock.New(mock.WithName("dup")))
	if !errors.Is(err, provider.ErrProviderExists) {
		t.Fatalf("duplicate Register() error = %v, want ErrProviderExists", err)
	}
}

func TestRegistry_RegisterInvalid(t *testing.T) {
	reg := provider.NewRegistry()

	if err := reg.Register(nil); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("Register(nil) = %v, want ErrInvalidRequest", err)
	}
	if err := reg.Register(mock.New(mock.WithName(""))); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("Register(empty name) = %v, want ErrInvalidRequest", err)
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	reg := provider.NewRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Errorf("Get(nope) ok = true, want false")
	}
}

func TestRegistry_Exists(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(mock.New(mock.WithName("beta")))

	if !reg.Exists("beta") {
		t.Errorf("Exists(beta) = false, want true")
	}
	if reg.Exists("gamma") {
		t.Errorf("Exists(gamma) = true, want false")
	}
}

func TestRegistry_ListAndNamesSorted(t *testing.T) {
	reg := provider.NewRegistry()
	// Register out of alphabetical order.
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		if err := reg.Register(mock.New(mock.WithName(n))); err != nil {
			t.Fatalf("Register(%s) = %v", n, err)
		}
	}

	wantNames := []string{"alpha", "bravo", "charlie"}

	names := reg.Names()
	if len(names) != len(wantNames) {
		t.Fatalf("Names() len = %d, want %d", len(names), len(wantNames))
	}
	for i, n := range wantNames {
		if names[i] != n {
			t.Errorf("Names()[%d] = %q, want %q", i, names[i], n)
		}
	}

	list := reg.List()
	for i, n := range wantNames {
		if list[i].Name() != n {
			t.Errorf("List()[%d].Name() = %q, want %q", i, list[i].Name(), n)
		}
	}

	if reg.Len() != 3 {
		t.Errorf("Len() = %d, want 3", reg.Len())
	}
}

func TestRegistry_ListReturnsCopy(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(mock.New(mock.WithName("only")))

	list := reg.List()
	list[0] = nil // mutate the returned slice

	if _, ok := reg.Get("only"); !ok {
		t.Errorf("mutating List() result affected the registry")
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := provider.NewRegistry()
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			name := "p" + itoa(i)
			_ = reg.Register(mock.New(mock.WithName(name)))
			_ = reg.Exists(name)
			_, _ = reg.Get(name)
			_ = reg.List()
		}(i)
	}
	wg.Wait()

	if reg.Len() != n {
		t.Errorf("Len() = %d, want %d after concurrent registration", reg.Len(), n)
	}
}

// itoa is a tiny helper to avoid importing strconv in the test for one call.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
