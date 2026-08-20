package lease

import (
	"leasereg/internal/store"
	"sort"
	"time"
)

func resourceAnalysis(leases []Lease, resources []store.ResourceMeta, nowTime time.Time) []ResourceAnalysis {
	held := map[string]Lease{}
	for _, item := range leases {
		held[item.Resource] = item
	}
	seen := map[string]bool{}
	out := make([]ResourceAnalysis, 0, len(resources)+len(held))
	for _, meta := range resources {
		item := ResourceAnalysis{Name: meta.Name, Registered: true, MaxTTL: meta.MaxTTL}
		if current, ok := held[meta.Name]; ok {
			item.Held = true
			item.Expired = current.Expired(nowTime)
			seen[meta.Name] = true
		}
		out = append(out, item)
	}
	for name, current := range held {
		if !seen[name] {
			out = append(out, ResourceAnalysis{Name: name, Held: true, Expired: current.Expired(nowTime)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
