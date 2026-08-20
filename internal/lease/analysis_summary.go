package lease

type AnalysisSummary struct {
	Active    int  `json:"active"`
	Expired   int  `json:"expired"`
	Holders   int  `json:"holders"`
	Resources int  `json:"resources"`
	Healthy   bool `json:"healthy"`
}

func (a Analysis) Summary() AnalysisSummary {
	return AnalysisSummary{Active: a.Active, Expired: a.Expired, Holders: len(a.Holders), Resources: len(a.Resources), Healthy: a.Healthy}
}
