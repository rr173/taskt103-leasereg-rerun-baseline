package httpapi

import (
	"net/http"
	"runtime"
)

// admin.go holds the resource-metadata, stats, version and admin-only
// endpoints. They are wired into the same mux as the lease endpoints; the
// separation is purely organisational.

// handleRegisterResource: POST /resources
func (s *Server) handleRegisterResource(w http.ResponseWriter, r *http.Request) {
	var req resourceRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	if err := s.mgr.RegisterResource(r.Context(), req.Name, req.Description, req.MaxTTL); err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "status": "registered"})
}

// handleListResources: GET /resources
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	resources, err := s.mgr.ListResources(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "list resources failed"})
		return
	}
	out := make([]map[string]any, len(resources))
	for i, m := range resources {
		out[i] = map[string]any{
			"name":            m.Name,
			"max_ttl_seconds": m.MaxTTL,
			"description":     m.Description,
			"created_at":      m.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetResource: GET /resources/{name}
func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m, ok, err := s.mgr.GetResource(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "resource not registered"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            m.Name,
		"max_ttl_seconds": m.MaxTTL,
		"description":     m.Description,
		"created_at":      m.CreatedAt,
	})
}

// handleUpdateResource: PUT /resources/{name}
func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req resourceRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	if err := s.mgr.UpdateResource(r.Context(), name, req.Description, req.MaxTTL); err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "status": "updated"})
}

// handleDeleteResource: DELETE /resources/{name}
func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.mgr.DeleteResource(r.Context(), name); err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStats: GET /stats
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.mgr.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "stats failed"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleForceRelease: DELETE /admin/leases/{resource}
func (s *Server) handleForceRelease(w http.ResponseWriter, r *http.Request) {
	resource := r.PathValue("resource")
	if err := s.mgr.ForceRelease(r.Context(), resource); err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleVersion: GET /version
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"name":       "leasereg",
		"version":    "1.0.0",
		"go_version": runtime.Version(),
	})
}
