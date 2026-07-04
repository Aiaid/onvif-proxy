// Package onvif implements the ONVIF Device and Media services for a single
// virtual device. Each device runs one http.Server bound to dev.Ports.SOAP
// and serves POST /onvif/device_service, POST /onvif/media_service and an
// unauthenticated GET /onvif/snapshot. Responses are hand-written XML
// templates matching the ONVIF WSDL element definitions; every dynamic value
// is XML-escaped. Method coverage follows docs/02 sections 4 and 5.
package onvif

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"github.com/Aiaid/onvif-proxy/internal/soap"
)

// clockSkew is the tolerance applied to the WSSE Created timestamp.
const clockSkew = 5 * time.Minute

// Options configures a Server. It carries values injected by main that cannot
// be derived from the device config alone.
type Options struct {
	// AdvertiseIP is the fallback host used to build URIs when a request
	// carries no Host header, and the address reported for eth0.
	AdvertiseIP string
	// Version is the firmware version shown by GetDeviceInformation.
	Version string
	// SnapshotFunc produces a JPEG for GET /onvif/snapshot. It is injected by
	// main (passthrough / ffmpeg / cache are composed there).
	SnapshotFunc func(ctx context.Context, streamName string) (data []byte, contentType string, err error)
}

// Server serves the ONVIF services for one device.
type Server struct {
	dev  *config.Device
	opts Options
	mux  *http.ServeMux
}

// NewServer builds a Server and its route table for dev.
func NewServer(dev *config.Device, opts Options) *Server {
	s := &Server{dev: dev, opts: opts, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /onvif/device_service", s.handleDevice)
	s.mux.HandleFunc("POST /onvif/media_service", s.handleMedia)
	s.mux.HandleFunc("GET /onvif/snapshot", s.handleSnapshot)
	return s
}

// Run listens on dev.Ports.SOAP and serves until ctx is cancelled, then shuts
// down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.dev.Ports.SOAP),
		Handler: s.mux,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errc:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// ---- request helpers ------------------------------------------------------

// parse reads and decodes the SOAP request body.
func parse(r *http.Request) (*soap.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return soap.ParseRequest(body)
}

// authorized reports whether the request may invoke action. When the device
// has no configured auth every request is allowed. GetSystemDateAndTime is
// always allowed so clients can synchronise their clock before computing a
// PasswordDigest.
func (s *Server) authorized(req *soap.Request, action string) bool {
	if s.dev.Auth == nil || action == "GetSystemDateAndTime" {
		return true
	}
	if req.Security == nil {
		return false
	}
	return req.Security.Verify(s.dev.Auth.Username, s.dev.Auth.Password, clockSkew)
}

// write renders an ONVIF response body inside a SOAP envelope.
func write(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", soap.ContentType)
	w.WriteHeader(http.StatusOK)
	w.Write(soap.Envelope(body))
}

// hostPort returns the Host header (including any port) or the advertise IP as
// a fallback. Used for HTTP XAddrs and the snapshot URI.
func (s *Server) hostPort(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	return s.opts.AdvertiseIP
}

// hostname returns just the host portion of the request (no port), falling
// back to the advertise IP. Used to build RTSP URIs.
func (s *Server) hostname(r *http.Request) string {
	h := r.Host
	if h == "" {
		return s.opts.AdvertiseIP
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	if h == "" {
		return s.opts.AdvertiseIP
	}
	return h
}

// advertiseIP returns the address reported for the eth0 interface.
func (s *Server) advertiseIP(r *http.Request) string {
	if s.opts.AdvertiseIP != "" {
		return s.opts.AdvertiseIP
	}
	return s.hostname(r)
}

// ---- snapshot -------------------------------------------------------------

// handleSnapshot serves GET /onvif/snapshot?token=<profileToken>. It is
// unauthenticated by design and delegates image production to SnapshotFunc.
func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	stream := s.dev.StreamByProfileToken(token)
	if stream == nil {
		http.Error(w, "unknown profile token", http.StatusNotFound)
		return
	}
	if s.opts.SnapshotFunc == nil {
		http.Error(w, "snapshot not available", http.StatusServiceUnavailable)
		return
	}
	data, contentType, err := s.opts.SnapshotFunc(r.Context(), stream.Name)
	if err != nil {
		http.Error(w, "snapshot failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ---- info defaults --------------------------------------------------------

func (s *Server) manufacturer() string {
	if s.dev.Info.Manufacturer != "" {
		return s.dev.Info.Manufacturer
	}
	return "ONVIF-Proxy"
}

func (s *Server) model() string {
	if s.dev.Info.Model != "" {
		return s.dev.Info.Model
	}
	return "Virtual-Camera"
}

func (s *Server) firmware() string {
	if s.dev.Info.Firmware != "" {
		return s.dev.Info.Firmware
	}
	if s.opts.Version != "" {
		return s.opts.Version
	}
	return "1.0.0"
}

// slug turns the device name into a hostname-safe label.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "camera"
	}
	return out
}
