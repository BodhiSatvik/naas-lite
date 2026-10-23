// Package api serves the naas-lite HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/example/naas-lite/internal/naas"
)

const maxBodyBytes = 1 << 20

// Server is the naas-lite HTTP handler.
type Server struct {
	store *naas.Store
	log   *slog.Logger
	mux   *http.ServeMux
}

// New returns a Server with its routes registered.
func New(store *naas.Store, log *slog.Logger) *Server {
	s := &Server{store: store, log: log, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/namespaces", s.createNamespace)
	s.mux.HandleFunc("GET /v1/namespaces", s.listNamespaces)
	s.mux.HandleFunc("GET /v1/namespaces/{name}", s.getNamespace)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

type namespaceResponse struct {
	Namespace     naas.Namespace     `json:"namespace"`
	ResourceQuota naas.ResourceQuota `json:"resourceQuota"`
}

type listResponse struct {
	Items []naas.Namespace `json:"items"`
}

func (s *Server) createNamespace(w http.ResponseWriter, r *http.Request) {
	var req naas.Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "the body must be a JSON namespace request: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		var ve *naas.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "validation failed")
		return
	}
	ns, err := s.store.Create(req)
	switch {
	case errors.Is(err, naas.ErrExists):
		writeError(w, http.StatusConflict, "already_exists", fmt.Sprintf("namespace %q already exists", req.Name))
		return
	case err != nil:
		s.log.Error("create namespace failed", "name", req.Name, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not create the namespace")
		return
	}
	s.log.Info("namespace created", "name", ns.Name, "team", ns.Team, "tier", ns.Tier, "cpu", ns.CPU, "memoryGi", ns.MemoryGi)
	writeJSON(w, http.StatusCreated, namespaceResponse{Namespace: ns, ResourceQuota: naas.QuotaFor(ns)})
}

func (s *Server) listNamespaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, listResponse{Items: s.store.List()})
}

func (s *Server) getNamespace(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ns, err := s.store.Get(name)
	if errors.Is(err, naas.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("namespace %q not found", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not read the namespace")
		return
	}
	writeJSON(w, http.StatusOK, namespaceResponse{Namespace: ns, ResourceQuota: naas.QuotaFor(ns)})
}
