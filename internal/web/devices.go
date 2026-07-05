package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"gopkg.in/yaml.v3"
)

// streamView is one Profile exposed by a device. Alongside the proxied
// endpoint (rtsp_uri) it carries the raw source parameters so the edit form can
// prefill without re-probing the camera. Exposing the upstream URL (with any
// embedded credentials) is consistent with GET /api/config, which already
// returns the configuration verbatim; web Basic auth gates both.
type streamView struct {
	Name         string `json:"name"`
	ProfileToken string `json:"profile_token"`
	RTSPURI      string `json:"rtsp_uri"`
	RTSP         string `json:"rtsp"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Framerate    int    `json:"framerate"`
	Bitrate      int    `json:"bitrate"`
}

type endpointsView struct {
	DeviceService string       `json:"device_service"`
	Snapshot      string       `json:"snapshot"`
	Streams       []streamView `json:"streams"`
}

type deviceView struct {
	Name      string        `json:"name"`
	UUID      string        `json:"uuid"`
	SOAPPort  int           `json:"soap_port"`
	RTSPPort  int           `json:"rtsp_port"`
	Running   bool          `json:"running"`
	AuthUser  string        `json:"auth_user"` // ONVIF WSSE username, "" when auth is off
	Endpoints endpointsView `json:"endpoints"`
}

// handleDevices maps the runtime device snapshot to the docs/04 JSON shape.
func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deviceViews())
}

// deviceViews builds the docs/04 device DTO list from the runtime snapshot,
// shared by GET /api/devices and the list_devices MCP tool.
func (s *Server) deviceViews() []deviceView {
	advertiseIP := s.backend.Status().AdvertiseIP
	runtimes := s.backend.Devices()

	out := make([]deviceView, 0, len(runtimes))
	for _, dr := range runtimes {
		dev := dr.Device
		if dev == nil {
			continue
		}
		streams := make([]streamView, 0, len(dev.Streams))
		for _, st := range dev.Streams {
			streams = append(streams, streamView{
				Name:         st.Name,
				ProfileToken: st.ProfileToken(),
				RTSPURI: fmt.Sprintf("rtsp://%s:%d%s",
					advertiseIP, dev.ProxyPortFor(st), st.PathQuery()),
				RTSP:      st.RTSP,
				Width:     st.Width,
				Height:    st.Height,
				Framerate: st.Framerate,
				Bitrate:   st.Bitrate,
			})
		}
		snapToken := ""
		if snap := dev.SnapshotStream(); snap != nil {
			snapToken = snap.ProfileToken()
		}
		authUser := ""
		if dev.Auth != nil {
			authUser = dev.Auth.Username
		}
		out = append(out, deviceView{
			Name:     dev.Name,
			UUID:     dev.UUID,
			SOAPPort: dev.Ports.SOAP,
			RTSPPort: dev.Ports.RTSP,
			Running:  dr.Running,
			AuthUser: authUser,
			Endpoints: endpointsView{
				DeviceService: fmt.Sprintf("http://%s:%d/onvif/device_service",
					advertiseIP, dev.Ports.SOAP),
				Snapshot: fmt.Sprintf("http://%s:%d/onvif/snapshot?token=%s",
					advertiseIP, dev.Ports.SOAP, snapToken),
				Streams: streams,
			},
		})
	}
	return out
}

// addDeviceRequest is the JSON body accepted by POST /api/devices. It mirrors a
// config.Device but leaves identity fields (uuid/mac/serial) to the hot-reload
// Load, which generates and persists them.
//
// Optional fields carry ",omitempty" not for marshaling (the struct is only
// ever decoded) but because the MCP add/update_device tool schema is inferred
// from these tags: without it every field would be marked required and a
// minimal add_device call would be rejected. A blank auth password stays legal
// for the edit flow's "unchanged" semantics.
type addDeviceRequest struct {
	Name     string `json:"name"`
	SOAPPort int    `json:"soap_port"`
	RTSPPort int    `json:"rtsp_port"`
	Auth     *struct {
		Username string `json:"username"`
		Password string `json:"password,omitempty"`
	} `json:"auth,omitempty"`
	Streams []struct {
		Name      string `json:"name"`
		RTSP      string `json:"rtsp"`
		Width     int    `json:"width,omitempty"`
		Height    int    `json:"height,omitempty"`
		Framerate int    `json:"framerate,omitempty"`
		Bitrate   int    `json:"bitrate,omitempty"`
	} `json:"streams"`
	Snapshot *struct {
		URL          string `json:"url,omitempty"`
		Stream       string `json:"stream,omitempty"`
		CacheSeconds int    `json:"cache_seconds,omitempty"`
	} `json:"snapshot,omitempty"`
}

// handleAddDevice appends a new device to the current config and hot-reloads it.
// The device is built from the posted form, merged into the current on-disk YAML
// (re-marshaled through config.Config), then handed to ApplyConfig. All field
// and port-conflict validation is performed by ApplyConfig's Parse; its errors
// are returned verbatim as 400 so the form can display them.
func (s *Server) handleAddDevice(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body failed", err.Error())
		return
	}
	var req addDeviceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body", err.Error())
		return
	}

	cfg, err := s.currentConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read config failed", err.Error())
		return
	}

	cfg.Devices = append(cfg.Devices, buildDevice(req))

	s.applyConfig(w, cfg)
}

// buildDevice constructs a config.Device from a posted form. Identity fields
// (uuid/mac/serial) and Info are intentionally left zero: on add they are
// generated by the hot-reload Load, and on edit they are copied from the
// original device by the caller.
func buildDevice(req addDeviceRequest) *config.Device {
	dev := &config.Device{
		Name:  req.Name,
		Ports: config.Ports{SOAP: req.SOAPPort, RTSP: req.RTSPPort},
	}
	if req.Auth != nil && req.Auth.Username != "" && req.Auth.Password != "" {
		dev.Auth = &config.Auth{Username: req.Auth.Username, Password: req.Auth.Password}
	}
	for _, st := range req.Streams {
		dev.Streams = append(dev.Streams, &config.Stream{
			Name:      st.Name,
			RTSP:      st.RTSP,
			Width:     st.Width,
			Height:    st.Height,
			Framerate: st.Framerate,
			Bitrate:   st.Bitrate,
		})
	}
	if req.Snapshot != nil {
		dev.Snapshot = config.Snapshot{
			URL:          req.Snapshot.URL,
			Stream:       req.Snapshot.Stream,
			CacheSeconds: req.Snapshot.CacheSeconds,
		}
	}
	return dev
}

// handleEditDevice replaces the device with the given uuid in-place and
// hot-reloads the config. The posted form carries no identity, so the original
// uuid/mac/serial and Info block are preserved; everything else (name, ports,
// auth, streams, snapshot) is rebuilt from the form. Unknown uuid returns 404;
// validation errors from ApplyConfig surface as 400 verbatim.
func (s *Server) handleEditDevice(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body failed", err.Error())
		return
	}
	var req addDeviceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body", err.Error())
		return
	}

	cfg, err := s.currentConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read config failed", err.Error())
		return
	}

	idx := -1
	for i, d := range cfg.Devices {
		if d.UUID == uuid {
			idx = i
			break
		}
	}
	if idx == -1 {
		writeErr(w, http.StatusNotFound, "device not found", "no configured device with that uuid")
		return
	}

	orig := cfg.Devices[idx]
	dev := buildDevice(req)
	dev.UUID = orig.UUID
	dev.MAC = orig.MAC
	dev.Serial = orig.Serial
	dev.Info = orig.Info
	// The DeviceRuntime DTO exposes only the ONVIF username, never the password,
	// so the edit form cannot round-trip it. A form that keeps a username but
	// leaves the password blank therefore means "unchanged", not "clear": carry
	// the original password over. Clearing the username still drops auth.
	if dev.Auth == nil && req.Auth != nil && req.Auth.Username != "" &&
		req.Auth.Password == "" && orig.Auth != nil {
		dev.Auth = &config.Auth{Username: req.Auth.Username, Password: orig.Auth.Password}
	}
	cfg.Devices[idx] = dev

	s.applyConfig(w, cfg)
}

// handleDeleteDevice removes the device with the given uuid from the current
// config and hot-reloads it. Unknown uuid returns 404.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	cfg, err := s.currentConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read config failed", err.Error())
		return
	}

	kept := make([]*config.Device, 0, len(cfg.Devices))
	found := false
	for _, d := range cfg.Devices {
		if d.UUID == uuid {
			found = true
			continue
		}
		kept = append(kept, d)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "device not found", "no configured device with that uuid")
		return
	}
	cfg.Devices = kept

	s.applyConfig(w, cfg)
}

// currentConfig fetches and strictly parses the on-disk YAML. The disk config is
// necessarily valid, so a parse error here is an internal fault.
func (s *Server) currentConfig() (*config.Config, error) {
	raw, err := s.backend.ConfigYAML()
	if err != nil {
		return nil, err
	}
	return config.Parse(raw)
}

// applyParsedConfig re-marshals cfg (2-space indent, matching config.Save) and
// hands it to the backend. Shared by the REST handlers and the MCP device tools;
// validation errors are returned verbatim for the caller to surface.
func (s *Server) applyParsedConfig(cfg *config.Config) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		enc.Close()
		return err
	}
	enc.Close()
	return s.backend.ApplyConfig(buf.Bytes(), false)
}

// applyConfig runs applyParsedConfig and maps its outcome to the REST response:
// a re-marshal failure is a 500, a rejected config a 400 with the full text.
func (s *Server) applyConfig(w http.ResponseWriter, cfg *config.Config) {
	if err := s.applyParsedConfig(cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "config rejected", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}
