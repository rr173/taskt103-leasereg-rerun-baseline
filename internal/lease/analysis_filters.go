package lease

import "time"

func filterActive(leases []Lease, nowTime time.Time) []Lease {
	out := make([]Lease, 0, len(leases))
	for _, item := range leases {
		if !item.Expired(nowTime) {
			out = append(out, item)
		}
	}
	return out
}
func filterExpired(leases []Lease, nowTime time.Time) []Lease {
	out := make([]Lease, 0, len(leases))
	for _, item := range leases {
		if item.Expired(nowTime) {
			out = append(out, item)
		}
	}
	return out
}
