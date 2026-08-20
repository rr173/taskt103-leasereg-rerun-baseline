package lease

func capacityWarnings(rows []ResourceAnalysis) []string {
	out := []string{}
	for _, row := range rows {
		if row.Registered && row.MaxTTL > 0 && row.Held && row.Expired {
			out = append(out, row.Name+":expired lease pending sweep")
		}
	}
	return out
}
