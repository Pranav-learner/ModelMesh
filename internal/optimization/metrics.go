package optimization

// ResourceMetrics is the optional observability seam for the optimization layer.
// It mirrors the routing and load balancer approach: the coordinator depends on
// this small interface, not on Prometheus, so it is observable without any
// metrics backend and the composition layer can bridge it into the catalog.
type ResourceMetrics interface {
	// BudgetUsage reports a budget's current usage and remaining balance (USD).
	BudgetUsage(scope, budgetID string, used, remaining float64)
	// Downgrade reports a model downgrade forced to stay within budget.
	Downgrade(fromModel, toModel string)
	// Reject reports a request rejected for exceeding its budget.
	Reject(scope, budgetID string)
	// LoadSelection reports which instance the load balancer chose (load
	// distribution + instance utilization).
	LoadSelection(providerName, instanceID string)
	// EstimatedSavings reports the estimated USD avoided by a downgrade.
	EstimatedSavings(usd float64)
}

// NopResourceMetrics is the no-op ResourceMetrics used by default.
type NopResourceMetrics struct{}

func (NopResourceMetrics) BudgetUsage(string, string, float64, float64) {}
func (NopResourceMetrics) Downgrade(string, string)                     {}
func (NopResourceMetrics) Reject(string, string)                        {}
func (NopResourceMetrics) LoadSelection(string, string)                 {}
func (NopResourceMetrics) EstimatedSavings(float64)                     {}
