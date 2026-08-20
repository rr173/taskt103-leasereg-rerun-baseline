package lease

import (
	"sort"
	"time"
)

func holderAnalysis(leases []Lease, nowTime time.Time) []HolderAnalysis {
	byHolder := map[string]*HolderAnalysis{}
	for _, item := range leases {
		result := byHolder[item.Holder]
		if result == nil {
			result = &HolderAnalysis{Holder: item.Holder}
			byHolder[item.Holder] = result
		}
		result.TotalTTL += item.TTLSeconds
		if item.Expired(nowTime) {
			result.Expired++
		} else {
			result.Active++
		}
	}
	out := make([]HolderAnalysis, 0, len(byHolder))
	for _, item := range byHolder {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Holder < out[j].Holder })
	return out
}
