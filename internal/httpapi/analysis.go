package httpapi

import (
	"leasereg/internal/lease"
	"leasereg/internal/leaseaudit"
	"net/http"
)

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	analysis, err := s.mgr.Analyze(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if err := analysis.Validate(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write([]byte(analysis.CSV()))
		return
	}
	analysis.Warnings = append(analysis.Warnings, analysis.HealthReasons()...)
	auditFindings := leaseaudit.Check(analysis)
	thresholds := lease.DefaultThresholds()
	writeJSON(w, http.StatusOK, map[string]any{
		"analysis":                 analysis,
		"summary":                  analysis.SummaryMap(),
		"metrics":                  analysis.Metrics(),
		"status":                   analysis.Status(),
		"recommendations":          analysis.Recommendations(),
		"expiry_window_seconds":    int64(analysis.ExpiryWindow(s.mgr.Clock().Now()).Seconds()),
		"thresholds":               thresholds,
		"breaches":                 analysis.Breaches(thresholds),
		"meets_default_thresholds": analysis.Meets(thresholds),
		"threshold_summary":        analysis.ThresholdSummary(thresholds),
		"fingerprint":              analysis.Fingerprint(),
		"has_leases":               analysis.HasLeases(),
		"resource_names":           analysis.ResourceNames(),
		"active_holders":           analysis.ActiveHolderNames(),
		"has_resources":            analysis.HasResources(),
		"ready_for_traffic":        analysis.ReadyForTraffic(),
		"audit_findings":           auditFindings,
	})
}
