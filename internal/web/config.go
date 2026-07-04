package web

import (
	"io"
	"net/http"
)

// handleGetConfig returns the raw config.yaml text for the editor.
func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	data, err := s.backend.ConfigYAML()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read config failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// handlePutConfig validates (and, unless ?dry_run=1, applies) posted YAML.
// Validation failures return 400 with the full error text so the UI can show
// each problem verbatim.
func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body failed", err.Error())
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "1"
	if err := s.backend.ApplyConfig(body, dryRun); err != nil {
		writeErr(w, http.StatusBadRequest, "config rejected", err.Error())
		return
	}
	msg := "applied"
	if dryRun {
		msg = "valid"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": msg})
}
