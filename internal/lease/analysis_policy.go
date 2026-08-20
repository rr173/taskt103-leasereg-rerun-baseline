package lease

import "fmt"

func (a Analysis) Validate() error {
	if a.Active < 0 || a.Expired < 0 {
		return fmt.Errorf("lease counts must be non-negative")
	}
	if a.Active+a.Expired < a.Active {
		return fmt.Errorf("lease count overflow")
	}
	for _, holder := range a.Holders {
		if holder.Active < 0 || holder.Expired < 0 || holder.TotalTTL < 0 {
			return fmt.Errorf("invalid holder analysis for %s", holder.Holder)
		}
	}
	for _, resource := range a.Resources {
		if resource.Name == "" {
			return fmt.Errorf("resource analysis contains an empty name")
		}
		if resource.MaxTTL < 0 {
			return fmt.Errorf("resource %s has a negative TTL cap", resource.Name)
		}
	}
	for _, bucket := range a.Expiry {
		if bucket.Count < 0 {
			return fmt.Errorf("expiry bucket %s is negative", bucket.Name)
		}
	}
	for _, token := range a.Tokens {
		if token.Next <= token.Current {
			return fmt.Errorf("resource %s has no next fencing token", token.Resource)
		}
	}
	return nil
}

func (a Analysis) Status() string {
	if a.HasCriticalRecommendation() {
		return "critical"
	}
	if len(a.Recommendations()) > 1 {
		return "attention"
	}
	return "healthy"
}

func (a Analysis) HasLeases() bool {
	return a.Active > 0 || a.Expired > 0
}

func (a Analysis) ResourceNames() []string {
	names := make([]string, 0, len(a.Resources))
	for _, resource := range a.Resources {
		names = append(names, resource.Name)
	}
	return names
}

func (a Analysis) ActiveHolderNames() []string {
	names := make([]string, 0)
	for _, holder := range a.Holders {
		if holder.Active > 0 {
			names = append(names, holder.Holder)
		}
	}
	return names
}

func (a Analysis) HasResources() bool { return len(a.Resources) > 0 }

func (a Analysis) ReadyForTraffic() bool { return a.Healthy && a.Validate() == nil }
