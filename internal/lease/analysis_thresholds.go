package lease

type Thresholds struct {
	MaxExpired  int `json:"max_expired"`
	MaxWarnings int `json:"max_warnings"`
	MinScore    int `json:"min_score"`
}

type ThresholdBreach struct {
	Name   string `json:"name"`
	Actual int    `json:"actual"`
	Limit  int    `json:"limit"`
}

func DefaultThresholds() Thresholds {
	return Thresholds{MaxExpired: 0, MaxWarnings: 0, MinScore: 80}
}

func (a Analysis) Breaches(thresholds Thresholds) []ThresholdBreach {
	breaches := make([]ThresholdBreach, 0)
	if a.Expired > thresholds.MaxExpired {
		breaches = append(breaches, ThresholdBreach{Name: "expired", Actual: a.Expired, Limit: thresholds.MaxExpired})
	}
	if len(a.Warnings) > thresholds.MaxWarnings {
		breaches = append(breaches, ThresholdBreach{Name: "warnings", Actual: len(a.Warnings), Limit: thresholds.MaxWarnings})
	}
	if a.OperationalScore() < thresholds.MinScore {
		breaches = append(breaches, ThresholdBreach{Name: "score", Actual: a.OperationalScore(), Limit: thresholds.MinScore})
	}
	return breaches
}

func (a Analysis) Meets(thresholds Thresholds) bool {
	return len(a.Breaches(thresholds)) == 0
}

func (a Analysis) PrefixMetrics(prefix string) []Metric {
	all := a.Metrics()
	filtered := make([]Metric, 0, len(all))
	for _, metric := range all {
		if prefix == "" || len(metric.Name) >= len(prefix) && metric.Name[:len(prefix)] == prefix {
			filtered = append(filtered, metric)
		}
	}
	return filtered
}

func (t Thresholds) Strict() bool {
	return t.MaxExpired == 0 && t.MaxWarnings == 0 && t.MinScore >= 80
}

func (t Thresholds) Names() []string {
	return []string{"expired", "warnings", "score"}
}

func (a Analysis) BreachNames(thresholds Thresholds) []string {
	breaches := a.Breaches(thresholds)
	names := make([]string, 0, len(breaches))
	for _, breach := range breaches {
		names = append(names, breach.Name)
	}
	return names
}

func (a Analysis) ThresholdSummary(thresholds Thresholds) map[string]any {
	return map[string]any{
		"strict":       thresholds.Strict(),
		"names":        thresholds.Names(),
		"breach_names": a.BreachNames(thresholds),
		"meets":        a.Meets(thresholds),
	}
}
