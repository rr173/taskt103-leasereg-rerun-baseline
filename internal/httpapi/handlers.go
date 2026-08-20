// Package httpapi exposes the lease registry over JSON/HTTP. It depends only
// on the lease domain package and the standard library; the net/http mux
// (Go 1.22+ method patterns) is used directly so no third-party router is
// pulled in.
//
// Error mapping:
//   - ErrConflict            -> 409 (with the conflicting lease)
//   - ErrExpired             -> 410 Gone
//   - ErrHolderMismatch      -> 403 Forbidden
//   - ErrTokenMismatch       -> 403 Forbidden
//   - ErrNoLease             -> 404 Not Found
//   - input validation errs -> 400 Bad Request
//   - anything else          -> 500 Internal Server Error
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"leasereg/internal/lease"
	"leasereg/internal/store"
)

// Server is the HTTP front-end for a lease.Manager.
type Server struct {
	mgr *lease.Manager
}

// New returns a Server wired to mgr.
func New(mgr *lease.Manager) *Server { return &Server{mgr: mgr} }

// leaseDTO is the JSON shape returned for a single lease. Timestamps are
// rendered as unix seconds so they survive any client timezone.
type leaseDTO struct {
	Resource     string `json:"resource"`
	Holder       string `json:"holder"`
	FencingToken int64  `json:"fencing_token"`
	AcquiredAt   int64  `json:"acquired_at"`
	ExpiresAt    int64  `json:"expires_at"`
	TTLSeconds   int64  `json:"ttl_seconds"`
	Expired      bool   `json:"expired,omitempty"`
}

type acquireRequest struct {
	Resource   string `json:"resource"`
	Holder     string `json:"holder"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type renewRequest struct {
	Resource     string `json:"resource"`
	Holder       string `json:"holder"`
	FencingToken int64  `json:"fencing_token"`
	TTLSeconds   int64  `json:"ttl_seconds"`
}

type releaseRequest struct {
	Resource     string `json:"resource"`
	Holder       string `json:"holder"`
	FencingToken int64  `json:"fencing_token"`
}

type transferRequest struct {
	Resource      string `json:"resource"`
	CurrentHolder string `json:"current_holder"`
	NewHolder     string `json:"new_holder"`
	FencingToken  int64  `json:"fencing_token"`
	TTLSeconds    int64  `json:"ttl_seconds"`
}

type resourceRequest struct {
	Name        string `json:"name"`
	MaxTTL      int64  `json:"max_ttl_seconds"`
	Description string `json:"description"`
}

type batchAcquireRequest struct {
	Requests []lease.AcquireItem `json:"requests"`
}

type errorResponse struct {
	Error    string    `json:"error"`
	Conflict *leaseDTO `json:"conflict,omitempty"`
}

type sweepResponse struct {
	Swept int `json:"swept"`
}

func toDTO(l lease.Lease, now int64) leaseDTO {
	return leaseDTO{
		Resource:     l.Resource,
		Holder:       l.Holder,
		FencingToken: l.FencingToken,
		AcquiredAt:   l.AcquiredAt.Unix(),
		ExpiresAt:    l.ExpiresAt.Unix(),
		TTLSeconds:   l.TTLSeconds,
		Expired:      l.ExpiresAt.Unix() <= now,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// mapError translates a domain error into an HTTP status code.
func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, lease.ErrEmptyResource):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, lease.ErrEmptyHolder):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, lease.ErrInvalidTTL):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, store.ErrEmptyResource):
		return http.StatusBadRequest, "resource name must not be empty"
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "resource held by an active lease"
	case errors.Is(err, store.ErrExpired):
		return http.StatusGone, "lease has expired"
	case errors.Is(err, store.ErrHolderMismatch):
		return http.StatusForbidden, "lease held by a different holder"
	case errors.Is(err, store.ErrTokenMismatch):
		return http.StatusForbidden, "fencing token does not match current lease"
	case errors.Is(err, store.ErrNoLease):
		return http.StatusNotFound, "no lease for resource"
	case errors.Is(err, store.ErrResourceNotFound):
		return http.StatusNotFound, "resource not registered"
	case errors.Is(err, store.ErrResourceInUse):
		return http.StatusConflict, "resource still has an active lease"
	case errors.Is(err, store.ErrTTLExceedsMax):
		return http.StatusBadRequest, "ttl_seconds exceeds resource max_ttl"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}

// handleAcquire: POST /acquire
func (s *Server) handleAcquire(w http.ResponseWriter, r *http.Request) {
	var req acquireRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	now := s.mgr.Clock().Now().Unix()
	granted, conflict, err := s.mgr.Acquire(r.Context(), req.Resource, req.Holder, req.TTLSeconds)
	if err != nil {
		status, msg := mapError(err)
		body := errorResponse{Error: msg}
		if conflict != nil {
			c := toDTO(*conflict, now)
			body.Conflict = &c
		}
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(granted, now))
}

// handleRenew: POST /renew
func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	var req renewRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	now := s.mgr.Clock().Now().Unix()
	renewed, err := s.mgr.Renew(r.Context(), req.Resource, req.Holder, req.FencingToken, req.TTLSeconds)
	if err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	writeJSON(w, http.StatusOK, toDTO(renewed, now))
}

// handleRelease: POST /release
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req releaseRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	if err := s.mgr.Release(r.Context(), req.Resource, req.Holder, req.FencingToken); err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleInfo: GET /info?resource=R
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	now := s.mgr.Clock().Now().Unix()
	l, ok, err := s.mgr.Info(r.Context(), resource)
	if err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no lease for resource"})
		return
	}
	writeJSON(w, http.StatusOK, toDTO(l, now))
}

// handleList: GET /leases
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	now := s.mgr.Clock().Now().Unix()
	leases, err := s.mgr.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "list failed"})
		return
	}
	out := make([]leaseDTO, len(leases))
	for i, l := range leases {
		out[i] = toDTO(l, now)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSweep: POST /admin/sweep
func (s *Server) handleSweep(w http.ResponseWriter, r *http.Request) {
	n, err := s.mgr.Sweep(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "sweep failed"})
		return
	}
	writeJSON(w, http.StatusOK, sweepResponse{Swept: n})
}

// handleHealth: GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTransfer: POST /transfer
func (s *Server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	now := s.mgr.Clock().Now().Unix()
	l, err := s.mgr.Transfer(r.Context(), req.Resource, req.CurrentHolder, req.FencingToken, req.TTLSeconds, req.NewHolder)
	if err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	writeJSON(w, http.StatusOK, toDTO(l, now))
}

// handleBatchAcquire: POST /batch/acquire
func (s *Server) handleBatchAcquire(w http.ResponseWriter, r *http.Request) {
	var req batchAcquireRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json: " + err.Error()})
		return
	}
	results := s.mgr.BatchAcquire(r.Context(), req.Requests)
	out := make([]map[string]any, len(results))
	now := s.mgr.Clock().Now().Unix()
	for i, res := range results {
		item := map[string]any{"resource": res.Resource}
		switch {
		case res.Granted != nil:
			dto := toDTO(*res.Granted, now)
			item["granted"] = dto
		case res.Conflict != nil:
			dto := toDTO(*res.Conflict, now)
			item["conflict"] = dto
			item["error"] = "resource held by an active lease"
		default:
			status, msg := mapError(res.Err)
			item["error"] = msg
			item["status"] = status
		}
		out[i] = item
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLeaseDetail: GET /leases/{resource}
func (s *Server) handleLeaseDetail(w http.ResponseWriter, r *http.Request) {
	resource := r.PathValue("resource")
	now := s.mgr.Clock().Now().Unix()
	l, ok, err := s.mgr.Info(r.Context(), resource)
	if err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no lease for resource"})
		return
	}
	writeJSON(w, http.StatusOK, toDTO(l, now))
}

// handleExpired: GET /expired
func (s *Server) handleExpired(w http.ResponseWriter, r *http.Request) {
	now := s.mgr.Clock().Now().Unix()
	leases, err := s.mgr.ListExpired(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "list expired failed"})
		return
	}
	out := make([]leaseDTO, len(leases))
	for i, l := range leases {
		out[i] = toDTO(l, now)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFencing: GET /fencing?resource=R
func (s *Server) handleFencing(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resource query param required"})
		return
	}
	tok, err := s.mgr.PeekFencingToken(r.Context(), resource)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resource": resource, "next_fencing_token": tok})
}

// handleHolderLeases: GET /holders/{holder}/leases
func (s *Server) handleHolderLeases(w http.ResponseWriter, r *http.Request) {
	holder := r.PathValue("holder")
	now := s.mgr.Clock().Now().Unix()
	leases, err := s.mgr.ListByHolder(r.Context(), holder)
	if err != nil {
		status, msg := mapError(err)
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	out := make([]leaseDTO, len(leases))
	for i, l := range leases {
		out[i] = toDTO(l, now)
	}
	writeJSON(w, http.StatusOK, out)
}
