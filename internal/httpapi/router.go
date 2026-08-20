package httpapi

import "net/http"

// NewRouter returns an *http.ServeMux with the lease-registry routes mounted.
// Go 1.22+ method-and-pattern routing is used so each handler is bound to a
// specific method without a separate dispatcher.
func NewRouter(s *Server) http.Handler {
	mux := http.NewServeMux()

	// Lease lifecycle
	mux.HandleFunc("POST /acquire", s.handleAcquire)
	mux.HandleFunc("POST /renew", s.handleRenew)
	mux.HandleFunc("POST /release", s.handleRelease)
	mux.HandleFunc("POST /transfer", s.handleTransfer)
	mux.HandleFunc("POST /batch/acquire", s.handleBatchAcquire)

	// Lease queries
	mux.HandleFunc("GET /info", s.handleInfo)
	mux.HandleFunc("GET /leases", s.handleList)
	mux.HandleFunc("GET /leases/{resource}", s.handleLeaseDetail)
	mux.HandleFunc("GET /expired", s.handleExpired)
	mux.HandleFunc("GET /fencing", s.handleFencing)
	mux.HandleFunc("GET /holders/{holder}/leases", s.handleHolderLeases)

	// Resource metadata
	mux.HandleFunc("POST /resources", s.handleRegisterResource)
	mux.HandleFunc("GET /resources", s.handleListResources)
	mux.HandleFunc("GET /resources/{name}", s.handleGetResource)
	mux.HandleFunc("PUT /resources/{name}", s.handleUpdateResource)
	mux.HandleFunc("DELETE /resources/{name}", s.handleDeleteResource)

	// Admin & diagnostics
	mux.HandleFunc("POST /admin/sweep", s.handleSweep)
	mux.HandleFunc("DELETE /admin/leases/{resource}", s.handleForceRelease)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /analysis", s.handleAnalysis)

	return mux
}
