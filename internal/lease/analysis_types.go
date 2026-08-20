package lease

type HolderAnalysis struct {
	Holder   string `json:"holder"`
	Active   int    `json:"active"`
	Expired  int    `json:"expired"`
	TotalTTL int64  `json:"total_ttl"`
}
type ResourceAnalysis struct {
	Name       string `json:"name"`
	Registered bool   `json:"registered"`
	MaxTTL     int64  `json:"max_ttl"`
	Held       bool   `json:"held"`
	Expired    bool   `json:"expired"`
}
type ExpiryBucket struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type TokenAnalysis struct {
	Resource  string `json:"resource"`
	Current   int64  `json:"current"`
	Next      int64  `json:"next"`
	Monotonic bool   `json:"monotonic"`
}
type Analysis struct {
	Holders   []HolderAnalysis   `json:"holders"`
	Resources []ResourceAnalysis `json:"resources"`
	Expiry    []ExpiryBucket     `json:"expiry"`
	Tokens    []TokenAnalysis    `json:"tokens"`
	Warnings  []string           `json:"warnings,omitempty"`
	Active    int                `json:"active"`
	Expired   int                `json:"expired"`
	Healthy   bool               `json:"healthy"`
	Durations DurationStats      `json:"durations"`
}
