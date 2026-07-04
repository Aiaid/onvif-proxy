// Package web serves the REST API and an embedded single-page management UI.
// It is a thin HTTP layer: RTSP probing, MJPEG preview and ffmpeg snapshots are
// delegated to sibling packages, while configuration read/write and the device
// runtime snapshot come from the Backend implemented by main.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"github.com/Aiaid/onvif-proxy/internal/discovery"
)

// all:static embeds the whole UI tree, including the built bundle under
// static/dist (the "all:" prefix keeps files the default rules would skip).
//
//go:embed all:static
var staticFiles embed.FS

// Backend is implemented by main; it wires the web layer to the live runtime.
type Backend interface {
	// ConfigYAML returns the current config.yaml text verbatim.
	ConfigYAML() ([]byte, error)
	// ApplyConfig parses raw YAML and, unless dryRun, persists and hot-reloads
	// it. Validation/parse errors are returned verbatim for the UI to display.
	ApplyConfig(raw []byte, dryRun bool) error
	// Devices returns a snapshot of the running devices.
	Devices() []DeviceRuntime
	// Snapshot grabs a single JPEG frame from the given device/stream.
	Snapshot(ctx context.Context, dev *config.Device, streamName string) ([]byte, string, error)
	// DiscoveryLog returns the recent WS-Discovery interactions.
	DiscoveryLog() []discovery.LogEntry
	// Status returns coarse runtime status for the top status bar.
	Status() Status
}

// DeviceRuntime pairs a configured device with its running state.
type DeviceRuntime struct {
	Device  *config.Device
	Running bool
}

// Status is the payload of GET /api/status.
type Status struct {
	Version       string `json:"version"`
	AdvertiseIP   string `json:"advertise_ip"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	FFmpeg        bool   `json:"ffmpeg"`
}

// Server owns the HTTP mux and per-device preview concurrency bookkeeping.
type Server struct {
	cfg     config.WebConfig
	backend Backend
	handler http.Handler

	previewMu  sync.Mutex
	previewSem map[string]int // device uuid -> active preview count
}

const maxPreviewPerDevice = 2

// New builds a Server. Basic authentication is wired in when a username is set.
func New(cfg config.WebConfig, backend Backend) *Server {
	s := &Server{
		cfg:        cfg,
		backend:    backend,
		previewSem: map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	mux.HandleFunc("GET /api/devices", s.handleDevices)
	mux.HandleFunc("POST /api/devices", s.handleAddDevice)
	mux.HandleFunc("PUT /api/devices/{uuid}", s.handleEditDevice)
	mux.HandleFunc("DELETE /api/devices/{uuid}", s.handleDeleteDevice)
	mux.HandleFunc("POST /api/test/rtsp", s.handleTestRTSP)
	mux.HandleFunc("POST /api/test/streaminfo", s.handleTestStreamInfo)
	mux.HandleFunc("GET /api/test/snapshot", s.handleTestSnapshot)
	mux.HandleFunc("GET /api/preview", s.handlePreview)
	mux.HandleFunc("POST /api/test/onvif", s.handleTestONVIF)
	mux.HandleFunc("GET /api/discovery/log", s.handleDiscoveryLog)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Serve the built bundle (dist/app.js, dist/app.css) straight from the
	// embedded FS; the sub-FS strips the "static/" prefix so /dist/* maps to
	// static/dist/*.
	if sub, err := fs.Sub(staticFiles, "static"); err == nil {
		mux.Handle("GET /dist/", http.FileServerFS(sub))
	}
	mux.HandleFunc("GET /", s.handleIndex)

	s.handler = s.withAuth(mux)
	return s
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.cfg.Port),
		Handler: s.handler,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// withAuth enforces HTTP Basic auth across every route when configured.
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.Username == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.cfg.Username || pass != s.cfg.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="onvif-proxy"`)
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "ui unavailable", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.Status())
}

func (s *Server) handleDiscoveryLog(w http.ResponseWriter, _ *http.Request) {
	log := s.backend.DiscoveryLog()
	if log == nil {
		log = []discovery.LogEntry{}
	}
	writeJSON(w, http.StatusOK, log)
}

// findDevice returns the running device with the given uuid, or nil.
func (s *Server) findDevice(uuid string) *config.Device {
	for _, dr := range s.backend.Devices() {
		if dr.Device != nil && dr.Device.UUID == uuid {
			return dr.Device
		}
	}
	return nil
}

// ---- response helpers ------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr emits the uniform {"error","detail"} error envelope.
func writeErr(w http.ResponseWriter, status int, msg, detail string) {
	writeJSON(w, status, map[string]string{"error": msg, "detail": detail})
}
