package optimization

import "errors"

// ErrBudgetExceeded indicates the budget engine rejected the request and no
// affordable downgrade was available. Callers match with errors.Is and must not
// dispatch the request. The Optimizer itself returns a non-error Plan with
// Rejected set; this sentinel is what the gateway surfaces to its caller.
var ErrBudgetExceeded = errors.New("budget exceeded")
