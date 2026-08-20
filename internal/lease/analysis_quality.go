package lease

func analysisHealthy(holders []HolderAnalysis, resources []ResourceAnalysis, tokens []TokenAnalysis) bool {
	for _, holder := range holders {
		if holder.Active < 0 || holder.Expired < 0 {
			return false
		}
	}
	for _, resource := range resources {
		if resource.MaxTTL < 0 || (resource.Expired && !resource.Held) {
			return false
		}
	}
	for _, token := range tokens {
		if !token.Monotonic || token.Next <= token.Current {
			return false
		}
	}
	return true
}
