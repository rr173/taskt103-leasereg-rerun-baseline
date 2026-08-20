package lease

import (
	"fmt"
	"sort"
)

type Metric struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
	Unit  string `json:"unit"`
}

// Metrics flattens the structured analysis into stable counters for monitors.
func (a Analysis) Metrics() []Metric {
	metrics := []Metric{
		{Name: "leases.active", Value: int64(a.Active), Unit: "leases"},
		{Name: "leases.expired", Value: int64(a.Expired), Unit: "leases"},
		{Name: "holders.total", Value: int64(len(a.Holders)), Unit: "holders"},
		{Name: "resources.total", Value: int64(len(a.Resources)), Unit: "resources"},
		{Name: "analysis.healthy", Value: boolMetric(a.Healthy), Unit: "boolean"},
	}
	for _, holder := range a.Holders {
		prefix := "holder." + holder.Holder
		metrics = append(metrics,
			Metric{Name: prefix + ".active", Value: int64(holder.Active), Unit: "leases"},
			Metric{Name: prefix + ".expired", Value: int64(holder.Expired), Unit: "leases"},
			Metric{Name: prefix + ".ttl_seconds", Value: holder.TotalTTL, Unit: "seconds"},
		)
	}
	for _, bucket := range a.Expiry {
		metrics = append(metrics, Metric{Name: "expiry." + bucket.Name, Value: int64(bucket.Count), Unit: "leases"})
	}
	for _, resource := range a.Resources {
		value := int64(0)
		if resource.Held {
			value = 1
		}
		metrics = append(metrics, Metric{Name: "resource." + resource.Name + ".held", Value: value, Unit: "boolean"})
		if resource.MaxTTL > 0 {
			metrics = append(metrics, Metric{Name: "resource." + resource.Name + ".max_ttl", Value: resource.MaxTTL, Unit: "seconds"})
		}
	}
	for _, token := range a.Tokens {
		metrics = append(metrics,
			Metric{Name: "token." + token.Resource + ".current", Value: token.Current, Unit: "token"},
			Metric{Name: "token." + token.Resource + ".next", Value: token.Next, Unit: "token"},
		)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	return metrics
}

func boolMetric(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// HealthReasons gives operators actionable reasons instead of only a boolean.
func (a Analysis) HealthReasons() []string {
	reasons := make([]string, 0, len(a.Warnings)+3)
	reasons = append(reasons, a.Warnings...)
	if a.Expired > 0 {
		reasons = append(reasons, fmt.Sprintf("%d leases require sweeping", a.Expired))
	}
	for _, token := range a.Tokens {
		if !token.Monotonic {
			reasons = append(reasons, "fencing token unavailable for "+token.Resource)
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "all lease invariants are healthy")
	}
	return reasons
}

// OperationalScore is a bounded signal for dashboards; it never changes the
// lease state and deliberately discounts expired rows rather than hiding them.
func (a Analysis) OperationalScore() int {
	score := 100
	score -= a.Expired * 5
	score -= len(a.Warnings) * 10
	for _, token := range a.Tokens {
		if !token.Monotonic {
			score -= 20
		}
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
