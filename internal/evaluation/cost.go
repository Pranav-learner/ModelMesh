package evaluation

import "github.com/symbiotes/modelmesh/internal/provider"

// CostModel computes the cost of a completed call from its model and reported
// token usage. It is a narrow interface so any pricing source can be injected;
// budget.PricingModel structurally satisfies it via its Actual method (adapt with
// CostModelFunc if the method name differs).
type CostModel interface {
	Cost(model string, usage provider.Usage) float64
}

// CostModelFunc adapts a plain function to the CostModel interface — e.g. to wrap
// budget.CostModel.Actual without importing budget here.
type CostModelFunc func(model string, usage provider.Usage) float64

// Cost calls the function.
func (f CostModelFunc) Cost(model string, usage provider.Usage) float64 { return f(model, usage) }

// zeroCost is the default cost model: it reports zero cost, so token differences
// are still computed but monetary comparison is neutral until a pricing model is
// wired.
type zeroCost struct{}

func (zeroCost) Cost(string, provider.Usage) float64 { return 0 }
