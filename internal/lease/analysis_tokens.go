package lease

import (
	"context"
	"leasereg/internal/store"
)

func (m *Manager) tokenAnalysis(ctx context.Context, resources []store.ResourceMeta) ([]TokenAnalysis, error) {
	out := make([]TokenAnalysis, 0, len(resources))
	for _, resource := range resources {
		next, err := m.PeekFencingToken(ctx, resource.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, TokenAnalysis{Resource: resource.Name, Current: next - 1, Next: next, Monotonic: next > 0})
	}
	return out, nil
}
