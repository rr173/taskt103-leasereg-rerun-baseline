package lease

import "time"

type DurationStats struct {
	Minimum int64 `json:"minimum"`
	Maximum int64 `json:"maximum"`
	Average int64 `json:"average"`
	Samples int   `json:"samples"`
}

func durationStats(leases []Lease) DurationStats {
	result := DurationStats{}
	if len(leases) == 0 {
		return result
	}
	result.Minimum = leases[0].TTLSeconds
	var total int64
	for _, item := range leases {
		if item.TTLSeconds < result.Minimum {
			result.Minimum = item.TTLSeconds
		}
		if item.TTLSeconds > result.Maximum {
			result.Maximum = item.TTLSeconds
		}
		total += item.TTLSeconds
	}
	result.Samples = len(leases)
	result.Average = total / int64(result.Samples)
	return result
}

func (a Analysis) ExpiryWindow(now time.Time) time.Duration {
	for _, bucket := range a.Expiry {
		if bucket.Name == "under_1m" && bucket.Count > 0 {
			return time.Minute
		}
	}
	return 0
}
