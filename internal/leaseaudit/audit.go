// Package leaseaudit turns the lease manager's operational snapshot into
// explicit, user-visible consistency findings for administrators.
package leaseaudit

import "leasereg/internal/lease"

// Finding describes one invariant violation discovered in an analysis
// snapshot. A clean snapshot returns an empty slice.
type Finding struct {
	Code     string `json:"code"`
	Resource string `json:"resource,omitempty"`
	Detail   string `json:"detail"`
}

// Check validates the cross-entity invariants that are useful during
// operations. Keeping this in its own package makes the audit policy
// reusable by HTTP and future offline recovery tooling.
func Check(snapshot lease.Analysis) []Finding {
	findings := make([]Finding, 0)
	for _, item := range snapshot.Resources {
		if item.Expired && !item.Held {
			findings = append(findings, Finding{
				Code:     "expired_without_lease",
				Resource: item.Name,
				Detail:   "resource metadata reports an expired lease without a stored lease",
			})
		}
	}
	for _, token := range snapshot.Tokens {
		if token.Current < 0 || token.Next <= token.Current || !token.Monotonic {
			findings = append(findings, Finding{
				Code:     "fencing_counter_invalid",
				Resource: token.Resource,
				Detail:   "fencing counter is not strictly ahead of the last allocated token",
			})
		}
	}
	return findings
}
