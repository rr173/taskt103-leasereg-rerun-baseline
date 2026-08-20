package lease

import "fmt"

type Recommendation struct {
	Area     string `json:"area"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func (a Analysis) Recommendations() []Recommendation {
	result := make([]Recommendation, 0)
	if a.Expired > 0 {
		result = append(result, Recommendation{Area: "recovery", Severity: "warning", Message: fmt.Sprintf("sweep %d expired leases before serving another renewal", a.Expired)})
	}
	for _, warning := range a.Warnings {
		result = append(result, Recommendation{Area: "resource", Severity: "warning", Message: warning})
	}
	for _, holder := range a.Holders {
		if holder.Expired > holder.Active {
			result = append(result, Recommendation{Area: "holder", Severity: "info", Message: "holder " + holder.Holder + " has more expired than active leases"})
		}
		if holder.TotalTTL == 0 && holder.Active > 0 {
			result = append(result, Recommendation{Area: "holder", Severity: "warning", Message: "holder " + holder.Holder + " has active leases with no recorded TTL"})
		}
	}
	for _, resource := range a.Resources {
		if resource.Registered && resource.MaxTTL == 0 && resource.Held {
			result = append(result, Recommendation{Area: "resource", Severity: "info", Message: "resource " + resource.Name + " is registered without a maximum TTL"})
		}
		if resource.Held && resource.Expired {
			result = append(result, Recommendation{Area: "resource", Severity: "warning", Message: "resource " + resource.Name + " is waiting for restart recovery or sweep"})
		}
	}
	for _, token := range a.Tokens {
		if !token.Monotonic {
			result = append(result, Recommendation{Area: "fencing", Severity: "critical", Message: "resource " + token.Resource + " cannot guarantee a next fencing token"})
		}
	}
	if len(result) == 0 {
		result = append(result, Recommendation{Area: "system", Severity: "info", Message: "no corrective action is required"})
	}
	return result
}

func (a Analysis) HasCriticalRecommendation() bool {
	for _, recommendation := range a.Recommendations() {
		if recommendation.Severity == "critical" {
			return true
		}
	}
	return false
}
