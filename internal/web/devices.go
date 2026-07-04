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

// streamView is one Profile exposed by a device.
type streamView struct {
	Name         string `json:"name"`
	ProfileToken string `json:"profile_token"`
	RTSPURI      string `json:"rtsp_uri"`
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
	Running   bool          `json:"running"`
	Endpoints endpointsView `json:"endpoints"`
}

// handleDevices maps the runtime device snapshot to the docs/04 JSON shape.
func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
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
			})
		}
		snapToken := ""
		if snap := dev.SnapshotStream(); snap != nil {
			snapToken = snap.ProfileToken()
		}
		out = append(out, deviceView{
			Name:     dev.Name,
			UUID:     dev.UUID,
			SOAPPort: dev.Ports.SOAP,
			Running:  dr.Running,
			Endpoints: endpointsView{
				DeviceService: fmt.Sprintf("http://%s:%d/onvif/device_service",
					advertiseIP, dev.Ports.SOAP),
				Snapshot: fmt.Sprintf("http://%s:%d/onvif/snapshot?token=%s",
					advertiseIP, dev.Ports.SOAP, snapToken),
				Streams: streams,
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// addDeviceRequest is the JSON body accepted by POST /api/devices. It mirrors a
// config.Device but leaves identity fields (uuid/mac/serial) to the hot-reload
// Load, which generates and persists them.
type addDeviceRequest struct {
	Name     string `json:"name"`
	SOAPPort int    `json:"soap_port"`
	RTSPPort int    `json:"rtsp_port"`
	Auth     *struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"auth"`
	Streams []struct {
		Name      string `json:"name"`
		RTSP      string `json:"rtsp"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Framerate int    `json:"framerate"`
		Bitrate   int    `json:"bitrate"`
	} `json:"streams"`
	Snapshot *struct {
		URL          string `json:"url"`
		Stream       string `json:"stream"`
		CacheSeconds int    `json:"cache_seconds"`
	} `json:"snapshot"`
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
	cfg.Devices = append(cfg.Devices, dev)

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

// applyConfig re-marshals cfg (2-space indent, matching config.Save) and hands
// it to the backend. Validation errors surface as 400 with the full text.
func (s *Server) applyConfig(w http.ResponseWriter, cfg *config.Config) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		enc.Close()
		writeErr(w, http.StatusInternalServerError, "encode config failed", err.Error())
		return
	}
	enc.Close()

	if err := s.backend.ApplyConfig(buf.Bytes(), false); err != nil {
		writeErr(w, http.StatusBadRequest, "config rejected", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}
