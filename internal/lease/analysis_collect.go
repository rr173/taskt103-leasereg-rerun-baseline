package lease

import (
	"context"
	"leasereg/internal/store"
	"sort"
)

func (m *Manager) collectAnalysis(ctx context.Context) ([]Lease, []store.ResourceMeta, error) {
	leases, err := m.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	resources, err := m.ListResources(ctx)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].Resource < leases[j].Resource })
	sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })
	return leases, resources, nil
}
