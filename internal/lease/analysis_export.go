package lease

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

func (a Analysis) Fingerprint() string {
	payload, err := json.Marshal(struct {
		Active  int
		Expired int
		Healthy bool
		Tokens  []TokenAnalysis
	}{Active: a.Active, Expired: a.Expired, Healthy: a.Healthy, Tokens: a.Tokens})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// CSV exports the same monitor metrics in a format suitable for batch
// diagnostics. Values are generated from the already-consistent snapshot.
func (a Analysis) CSV() string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"name", "value", "unit"})
	for _, metric := range a.Metrics() {
		_ = w.Write([]string{metric.Name, strconv.FormatInt(metric.Value, 10), metric.Unit})
	}
	w.Flush()
	return b.String()
}

func (a Analysis) SummaryMap() map[string]any {
	return map[string]any{
		"active":  a.Active,
		"expired": a.Expired,
		"healthy": a.Healthy,
		"score":   a.OperationalScore(),
		"reasons": a.HealthReasons(),
	}
}
