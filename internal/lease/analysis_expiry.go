package lease

import "time"

func expiryBuckets(leases []Lease, nowTime time.Time) []ExpiryBucket {
	counts := map[string]int{"expired": 0, "under_1m": 0, "under_5m": 0, "later": 0}
	for _, item := range leases {
		remaining := item.ExpiresAt.Sub(nowTime)
		switch {
		case remaining <= 0:
			counts["expired"]++
		case remaining <= time.Minute:
			counts["under_1m"]++
		case remaining <= 5*time.Minute:
			counts["under_5m"]++
		default:
			counts["later"]++
		}
	}
	ordered := []string{"expired", "under_1m", "under_5m", "later"}
	out := make([]ExpiryBucket, 0, len(ordered))
	for _, name := range ordered {
		out = append(out, ExpiryBucket{Name: name, Count: counts[name]})
	}
	return out
}
