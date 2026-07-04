package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/mediautil"
	"github.com/Aiaid/onvif-proxy/internal/rtsp"
)

// handleTestRTSP probes an arbitrary rtsp:// URL. Only the RTSP scheme is
// accepted so this endpoint cannot be turned into a generic port scanner.
func (s *Server) handleTestRTSP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body", err.Error())
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Scheme != "rtsp" || u.Host == "" {
		writeErr(w, http.StatusBadRequest, "invalid url", "url must be a valid rtsp:// URL")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, toRTSPView(rtsp.Probe(ctx, req.URL)))
}

// rtspView is the snake_case JSON shape documented in docs/04. The rtsp.Result
// struct carries no JSON tags, so we map it here rather than emitting Go field
// names the UI cannot read.
type rtspView struct {
	OK        bool        `json:"ok"`
	Status    int         `json:"status"`
	Auth      string      `json:"auth"`
	Server    string      `json:"server"`
	LatencyMS int64       `json:"latency_ms"`
	Tracks    []trackView `json:"tracks"`
	ErrKind   string      `json:"err_kind,omitempty"`
	ErrDetail string      `json:"err_detail,omitempty"`
}

type trackView struct {
	Type  string `json:"type"`
	Codec string `json:"codec"`
	Fmtp  string `json:"fmtp"`
}

func toRTSPView(res *rtsp.Result) rtspView {
	v := rtspView{
		OK:        res.OK,
		Status:    res.Status,
		Auth:      res.Auth,
		Server:    res.Server,
		LatencyMS: res.LatencyMS,
		ErrKind:   string(res.ErrKind),
		ErrDetail: res.ErrDetail,
		Tracks:    make([]trackView, 0, len(res.Tracks)),
	}
	for _, t := range res.Tracks {
		v.Tracks = append(v.Tracks, trackView{Type: t.Type, Codec: t.Codec, Fmtp: t.Fmtp})
	}
	return v
}

// handleTestStreamInfo runs ffprobe against an rtsp:// URL and returns the
// first video stream's codec, resolution and frame rate, used by the add-device
// form to auto-fill width/height/framerate. Only the RTSP scheme is accepted.
// When ffmpeg (and thus ffprobe) is unavailable it returns 501.
func (s *Server) handleTestStreamInfo(w http.ResponseWriter, r *http.Request) {
	if !mediautil.Available() {
		writeErr(w, http.StatusNotImplemented, "ffmpeg unavailable",
			"ffprobe is required for stream probing but is not on PATH")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body", err.Error())
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Scheme != "rtsp" || u.Host == "" {
		writeErr(w, http.StatusBadRequest, "invalid url", "url must be a valid rtsp:// URL")
		return
	}
	// ffprobe metadata plus the ~3s bitrate sampling fallback; kept under the
	// frontend's 20s fetch timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	info, err := mediautil.ProbeInfo(ctx, req.URL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "probe failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleTestSnapshot grabs a single JPEG frame from a device stream.
func (s *Server) handleTestSnapshot(w http.ResponseWriter, r *http.Request) {
	dev := s.findDevice(r.URL.Query().Get("device"))
	if dev == nil {
		writeErr(w, http.StatusNotFound, "device not found", "no running device with that uuid")
		return
	}
	streamName := r.URL.Query().Get("stream")
	data, contentType, err := s.backend.Snapshot(r.Context(), dev, streamName)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "snapshot failed", err.Error())
		return
	}
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

// handlePreview streams an MJPEG preview, capped at two concurrent previews
// per device. Over the cap it returns 429 instead of spawning more ffmpeg.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	uuid := r.URL.Query().Get("device")
	dev := s.findDevice(uuid)
	if dev == nil {
		writeErr(w, http.StatusNotFound, "device not found", "no running device with that uuid")
		return
	}
	stream := dev.PrimaryStream()
	if name := r.URL.Query().Get("stream"); name != "" {
		stream = dev.StreamByName(name)
	}
	if stream == nil {
		writeErr(w, http.StatusNotFound, "stream not found", "no such stream on device")
		return
	}
	if !s.acquirePreview(uuid) {
		writeErr(w, http.StatusTooManyRequests, "too many previews",
			"at most two concurrent previews per device")
		return
	}
	defer s.releasePreview(uuid)

	_ = mediautil.ServeMJPEG(w, r, stream.SourceURL(), 640, 5)
}

func (s *Server) acquirePreview(uuid string) bool {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.previewSem[uuid] >= maxPreviewPerDevice {
		return false
	}
	s.previewSem[uuid]++
	return true
}

func (s *Server) releasePreview(uuid string) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.previewSem[uuid] > 0 {
		s.previewSem[uuid]--
	}
}
