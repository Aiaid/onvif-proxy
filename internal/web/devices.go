package web

import (
	"fmt"
	"net/http"
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
