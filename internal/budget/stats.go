package budget

import "sort"

// Statistics is an overview of every tracked budget and the running spend, a read
// model safe to serialize and log.
type Statistics struct {
	Policy       string         `json:"policy"`
	TotalBudgets int            `json:"total_budgets"`
	TotalSpent   float64        `json:"total_spent"`
	Budgets      []BudgetStatus `json:"budgets"`
}

// Statistics returns a snapshot of all tracked budgets (with any elapsed windows
// rolled over) and the aggregate spend across them.
func (m *Manager) Statistics() Statistics {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	stats := Statistics{
		Policy:       m.policy.Name(),
		TotalBudgets: len(m.budgets),
		Budgets:      make([]BudgetStatus, 0, len(m.budgets)),
	}
	for _, mb := range m.budgets {
		mb.rollover(now)
		s := mb.status()
		stats.TotalSpent += s.CurrentUsage
		stats.Budgets = append(stats.Budgets, s)
	}
	sort.Slice(stats.Budgets, func(i, j int) bool {
		if stats.Budgets[i].Scope != stats.Budgets[j].Scope {
			return stats.Budgets[i].Scope < stats.Budgets[j].Scope
		}
		return stats.Budgets[i].ID < stats.Budgets[j].ID
	})
	return stats
}
