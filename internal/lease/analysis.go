package lease

import "context"

// Analyze returns a read-only operational view combining lease liveness,
// resource registration, expiry pressure and fencing-token readiness.
func (m *Manager) Analyze(ctx context.Context) (Analysis, error) {
	leases, resources, err := m.collectAnalysis(ctx)
	if err != nil {
		return Analysis{}, err
	}
	nowTime := m.clock.Now()
	holders := holderAnalysis(leases, nowTime)
	resourceRows := resourceAnalysis(leases, resources, nowTime)
	tokens, err := m.tokenAnalysis(ctx, resources)
	if err != nil {
		return Analysis{}, err
	}
	active, expired := filterActive(leases, nowTime), filterExpired(leases, nowTime)
	result := Analysis{Holders: holders, Resources: resourceRows, Expiry: expiryBuckets(leases, nowTime), Tokens: tokens, Warnings: capacityWarnings(resourceRows), Active: len(active), Expired: len(expired), Healthy: true}
	result.Durations = durationStats(leases)
	result.Healthy = analysisHealthy(holders, resourceRows, tokens)
	return result, nil
}
