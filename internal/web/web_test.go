package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"github.com/Aiaid/onvif-proxy/internal/discovery"
)

const testYAML = `
web:
  port: 8080
devices:
  - name: cam1
    uuid: 11111111-2222-3333-4444-555555555555
    ports:
      soap: 8081
      rtsp: 8554
    streams:
      - name: main
        rtsp: rtsp://user:pass@192.168.1.50:554/ch1/main
        width: 1920
        height: 1080
        framerate: 25
        bitrate: 4096
`

// mockBackend is a minimal Backend for HTTP-layer tests.
type mockBackend struct {
	yaml       []byte
	applyErr   error
	applied    []byte
	appliedDry bool
	devices    []DeviceRuntime
}

func (m *mockBackend) ConfigYAML() ([]byte, error) { return m.yaml, nil }
func (m *mockBackend) ApplyConfig(raw []byte, dryRun bool) error {
	m.applied = raw
	m.appliedDry = dryRun
	return m.applyErr
}
func (m *mockBackend) Devices() []DeviceRuntime { return m.devices }
func (m *mockBackend) Snapshot(_ context.Context, _ *config.Device, _ string) ([]byte, string, error) {
	return []byte{0xff, 0xd8}, "image/jpeg", nil
}
func (m *mockBackend) DiscoveryLog() []discovery.LogEntry { return nil }
func (m *mockBackend) Status() Status {
	return Status{Version: "test", AdvertiseIP: "10.0.0.1", FFmpeg: true}
}

func newTestBackend(t *testing.T) *mockBackend {
	t.Helper()
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("parse test yaml: %v", err)
	}
	devs := make([]DeviceRuntime, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		devs = append(devs, DeviceRuntime{Device: d, Running: true})
	}
	return &mockBackend{yaml: []byte(testYAML), devices: devs}
}

func do(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func TestGetConfig(t *testing.T) {
	s := New(config.WebConfig{Port: 8080}, newTestBackend(t))
	rec := do(s, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cam1") {
		t.Fatalf("body missing config: %q", rec.Body.String())
	}
}

func TestPutConfigDryRun(t *testing.T) {
	b := newTestBackend(t)
	s := New(config.WebConfig{Port: 8080}, b)
	req := httptest.NewRequest(http.MethodPut, "/api/config?dry_run=1", strings.NewReader("x: 1"))
	rec := do(s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !b.appliedDry {
		t.Fatalf("expected dry-run flag to be set")
	}
}

func TestDevices(t *testing.T) {
	s := New(config.WebConfig{Port: 8080}, newTestBackend(t))
	rec := do(s, httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []deviceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "cam1" {
		t.Fatalf("unexpected devices: %+v", got)
	}
	st := got[0].Endpoints.Streams
	if len(st) != 1 || st[0].ProfileToken != "profile_main" {
		t.Fatalf("unexpected streams: %+v", st)
	}
	want := "rtsp://10.0.0.1:8554/ch1/main"
	if st[0].RTSPURI != want {
		t.Fatalf("rtsp_uri = %q, want %q", st[0].RTSPURI, want)
	}
}

func TestBasicAuthChallenge(t *testing.T) {
	s := New(config.WebConfig{Port: 8080, Username: "admin", Password: "secret"}, newTestBackend(t))

	rec := do(s, httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("missing WWW-Authenticate header")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.SetBasicAuth("admin", "secret")
	if rec := do(s, req); rec.Code != http.StatusOK {
		t.Fatalf("authed status = %d", rec.Code)
	}
}

func TestTestRTSPRejectsNonRTSPScheme(t *testing.T) {
	s := New(config.WebConfig{Port: 8080}, newTestBackend(t))
	req := httptest.NewRequest(http.MethodPost, "/api/test/rtsp",
		strings.NewReader(`{"url":"http://example.com/x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := do(s, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTestStreamInfoRejectsNonRTSPScheme(t *testing.T) {
	s := New(config.WebConfig{Port: 8080}, newTestBackend(t))
	req := httptest.NewRequest(http.MethodPost, "/api/test/streaminfo",
		strings.NewReader(`{"url":"http://example.com/x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := do(s, req)
	// When ffmpeg is unavailable the handler short-circuits with 501 before the
	// scheme check; both outcomes reject the request, which is what we assert.
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 400 or 501", rec.Code)
	}
}

const addDeviceBody = `{
  "name": "cam2",
  "soap_port": 8082,
  "rtsp_port": 8555,
  "auth": {"username": "admin", "password": "secret"},
  "streams": [
    {"name": "main", "rtsp": "rtsp://user:pass@192.168.1.60:554/ch1/main",
     "width": 1280, "height": 720, "framerate": 20, "bitrate": 2048}
  ],
  "snapshot": {"url": "", "stream": "", "cache_seconds": 0}
}`

func TestAddDeviceSuccess(t *testing.T) {
	b := newTestBackend(t)
	s := New(config.WebConfig{Port: 8080}, b)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(addDeviceBody))
	req.Header.Set("Content-Type", "application/json")
	rec := do(s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if b.applied == nil {
		t.Fatal("ApplyConfig was not called")
	}
	if b.appliedDry {
		t.Fatal("device add must not be a dry run")
	}
	applied := string(b.applied)
	// The new device is present and the original device is preserved.
	if !strings.Contains(applied, "cam2") {
		t.Fatalf("applied yaml missing new device:\n%s", applied)
	}
	if !strings.Contains(applied, "cam1") {
		t.Fatalf("applied yaml dropped existing device:\n%s", applied)
	}
	// The applied YAML must itself re-parse cleanly (2-space indent, valid shape).
	if _, err := config.Parse(b.applied); err != nil {
		t.Fatalf("applied yaml does not re-parse: %v\n%s", err, applied)
	}
}

func TestAddDevicePortConflict(t *testing.T) {
	b := newTestBackend(t)
	b.applyErr = &config.ValidationError{Problems: []string{"port 8081 used by both web and devices"}}
	s := New(config.WebConfig{Port: 8080}, b)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(addDeviceBody))
	req.Header.Set("Content-Type", "application/json")
	rec := do(s, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "port 8081") {
		t.Fatalf("validation detail not surfaced: %s", rec.Body.String())
	}
}

func TestDeleteDeviceUnknownUUID(t *testing.T) {
	b := newTestBackend(t)
	s := New(config.WebConfig{Port: 8080}, b)
	req := httptest.NewRequest(http.MethodDelete, "/api/devices/00000000-0000-0000-0000-000000000000", nil)
	rec := do(s, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if b.applied != nil {
		t.Fatal("ApplyConfig must not be called for unknown uuid")
	}
}

func TestDeleteDeviceSuccess(t *testing.T) {
	b := newTestBackend(t)
	s := New(config.WebConfig{Port: 8080}, b)
	req := httptest.NewRequest(http.MethodDelete, "/api/devices/11111111-2222-3333-4444-555555555555", nil)
	rec := do(s, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if b.applied == nil {
		t.Fatal("ApplyConfig was not called")
	}
	if strings.Contains(string(b.applied), "cam1") {
		t.Fatalf("deleted device still present:\n%s", b.applied)
	}
}
