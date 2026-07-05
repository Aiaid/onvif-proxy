package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"github.com/Aiaid/onvif-proxy/internal/discovery"
	"github.com/Aiaid/onvif-proxy/internal/mediautil"
	"github.com/Aiaid/onvif-proxy/internal/rtsp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpInstructions is the server-level guidance handed to MCP clients on
// initialize (docs/07 §1). It orients the model toward the intended workflow.
const mcpInstructions = "onvif-proxy management server: wrap RTSP streams as virtual ONVIF cameras. " +
	"Use list_devices/get_status to inspect, probe_rtsp/get_stream_info before adding a device, " +
	"add_device/update_device/delete_device to manage, run_onvif_selftest to verify."

// mcpHandler builds the Streamable HTTP endpoint mounted at /mcp. The SDK owns
// the whole protocol surface (lifecycle, SSE, method negotiation); we only
// register the tool set. A fresh mcp.Server is built per request because the
// transport is stateless — the SDK's getServer may legitimately be called for
// every incoming session.
func (s *Server) mcpHandler() http.Handler {
	getServer := func(*http.Request) *mcp.Server {
		srv := mcp.NewServer(&mcp.Implementation{
			Name:    "onvif-proxy",
			Version: s.backend.Status().Version,
		}, &mcp.ServerOptions{Instructions: mcpInstructions})

		mcp.AddTool(srv, &mcp.Tool{Name: "get_status",
			Description: "Coarse runtime status: version, advertise IP, uptime and whether ffmpeg is available."}, s.mcpGetStatus)
		mcp.AddTool(srv, &mcp.Tool{Name: "list_devices",
			Description: "List the running virtual ONVIF devices with their ONVIF/RTSP endpoints."}, s.mcpListDevices)
		mcp.AddTool(srv, &mcp.Tool{Name: "get_config",
			Description: "Return the current config.yaml text verbatim."}, s.mcpGetConfig)
		mcp.AddTool(srv, &mcp.Tool{Name: "apply_config",
			Description: "Validate and (unless dry_run) persist and hot-reload a full config.yaml. Rejected configs return the validation error."}, s.mcpApplyConfig)
		mcp.AddTool(srv, &mcp.Tool{Name: "add_device",
			Description: "Append a virtual device built from the given definition and hot-reload."}, s.mcpAddDevice)
		mcp.AddTool(srv, &mcp.Tool{Name: "update_device",
			Description: "Replace the device with the given uuid, preserving its identity (uuid/mac/serial/info). A blank password with an unchanged username keeps the existing password."}, s.mcpUpdateDevice)
		mcp.AddTool(srv, &mcp.Tool{Name: "delete_device",
			Description: "Remove the device with the given uuid and hot-reload."}, s.mcpDeleteDevice)
		mcp.AddTool(srv, &mcp.Tool{Name: "probe_rtsp",
			Description: "Probe an rtsp:// URL and report reachability, auth mode and advertised tracks."}, s.mcpProbeRTSP)
		mcp.AddTool(srv, &mcp.Tool{Name: "get_stream_info",
			Description: "Run ffprobe against an rtsp:// URL and return the first video stream's codec, resolution, frame rate and bitrate. Requires ffmpeg."}, s.mcpGetStreamInfo)
		mcp.AddTool(srv, &mcp.Tool{Name: "take_snapshot",
			Description: "Grab a single JPEG frame from a device stream, returned as an image. Requires ffmpeg."}, s.mcpTakeSnapshot)
		mcp.AddTool(srv, &mcp.Tool{Name: "run_onvif_selftest",
			Description: "Exercise the proxy's own ONVIF services for a device and report the per-method pass/fault matrix."}, s.mcpRunSelfTest)
		mcp.AddTool(srv, &mcp.Tool{Name: "get_discovery_log",
			Description: "Return the recent WS-Discovery interactions."}, s.mcpGetDiscoveryLog)

		return srv
	}

	// The SDK applies no cross-origin protection by default (its built-in
	// CrossOriginProtection is deprecated and nil-defaulted), so we guard the
	// endpoint ourselves against DNS-rebinding as the MCP spec requires.
	return withOriginCheck(mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true}))
}

// ---- tool inputs -----------------------------------------------------------

// noInput is the empty object schema for parameterless tools.
type noInput struct{}

type urlInput struct {
	URL string `json:"url" jsonschema:"an rtsp:// URL"`
}

type applyConfigInput struct {
	YAML   string `json:"yaml" jsonschema:"full config.yaml text to validate and apply"`
	DryRun bool   `json:"dry_run" jsonschema:"when true, only validate without persisting"`
}

type addDeviceInput struct {
	Device addDeviceRequest `json:"device" jsonschema:"device definition, same shape as the POST /api/devices body"`
}

type updateDeviceInput struct {
	UUID   string           `json:"uuid" jsonschema:"uuid of the device to update"`
	Device addDeviceRequest `json:"device" jsonschema:"new device definition; identity fields are preserved from the existing device"`
}

type uuidInput struct {
	UUID string `json:"uuid" jsonschema:"device uuid"`
}

type snapshotInput struct {
	UUID   string `json:"uuid" jsonschema:"device uuid"`
	Stream string `json:"stream,omitempty" jsonschema:"optional stream name; defaults to the snapshot/primary stream"`
}

// ---- tool handlers ---------------------------------------------------------
//
// Business failures return a non-nil error: the SDK's ToolHandlerFor packs it
// into an isError result with the error text as content, which is exactly the
// docs/07 semantics. Malformed arguments are rejected by the SDK before ever
// reaching these handlers.

func (s *Server) mcpGetStatus(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	return mcpJSON(s.backend.Status()), nil, nil
}

func (s *Server) mcpListDevices(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	return mcpJSON(s.deviceViews()), nil, nil
}

func (s *Server) mcpGetConfig(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	raw, err := s.backend.ConfigYAML()
	if err != nil {
		return nil, nil, err
	}
	return mcpJSON(string(raw)), nil, nil
}

func (s *Server) mcpApplyConfig(_ context.Context, _ *mcp.CallToolRequest, in applyConfigInput) (*mcp.CallToolResult, any, error) {
	if err := s.backend.ApplyConfig([]byte(in.YAML), in.DryRun); err != nil {
		return nil, nil, err
	}
	if in.DryRun {
		return mcpJSON("valid"), nil, nil
	}
	return mcpJSON("applied"), nil, nil
}

func (s *Server) mcpAddDevice(_ context.Context, _ *mcp.CallToolRequest, in addDeviceInput) (*mcp.CallToolResult, any, error) {
	cfg, err := s.currentConfig()
	if err != nil {
		return nil, nil, err
	}
	cfg.Devices = append(cfg.Devices, buildDevice(in.Device))
	if err := s.applyParsedConfig(cfg); err != nil {
		return nil, nil, err
	}
	return mcpJSON("applied"), nil, nil
}

func (s *Server) mcpUpdateDevice(_ context.Context, _ *mcp.CallToolRequest, in updateDeviceInput) (*mcp.CallToolResult, any, error) {
	cfg, err := s.currentConfig()
	if err != nil {
		return nil, nil, err
	}
	idx := -1
	for i, d := range cfg.Devices {
		if d.UUID == in.UUID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, nil, fmt.Errorf("no configured device with that uuid")
	}

	orig := cfg.Devices[idx]
	dev := buildDevice(in.Device)
	dev.UUID = orig.UUID
	dev.MAC = orig.MAC
	dev.Serial = orig.Serial
	dev.Info = orig.Info
	// Same "blank password = unchanged" rule as handleEditDevice: the device DTO
	// never exposes the ONVIF password, so a kept username with an empty password
	// means "leave it alone", not "clear it".
	if dev.Auth == nil && in.Device.Auth != nil && in.Device.Auth.Username != "" &&
		in.Device.Auth.Password == "" && orig.Auth != nil {
		dev.Auth = &config.Auth{Username: in.Device.Auth.Username, Password: orig.Auth.Password}
	}
	cfg.Devices[idx] = dev

	if err := s.applyParsedConfig(cfg); err != nil {
		return nil, nil, err
	}
	return mcpJSON("applied"), nil, nil
}

func (s *Server) mcpDeleteDevice(_ context.Context, _ *mcp.CallToolRequest, in uuidInput) (*mcp.CallToolResult, any, error) {
	cfg, err := s.currentConfig()
	if err != nil {
		return nil, nil, err
	}
	kept := make([]*config.Device, 0, len(cfg.Devices))
	found := false
	for _, d := range cfg.Devices {
		if d.UUID == in.UUID {
			found = true
			continue
		}
		kept = append(kept, d)
	}
	if !found {
		return nil, nil, fmt.Errorf("no configured device with that uuid")
	}
	cfg.Devices = kept

	if err := s.applyParsedConfig(cfg); err != nil {
		return nil, nil, err
	}
	return mcpJSON("applied"), nil, nil
}

func (s *Server) mcpProbeRTSP(ctx context.Context, _ *mcp.CallToolRequest, in urlInput) (*mcp.CallToolResult, any, error) {
	if err := validateRTSPURL(in.URL); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return mcpJSON(toRTSPView(rtsp.Probe(ctx, in.URL))), nil, nil
}

func (s *Server) mcpGetStreamInfo(ctx context.Context, _ *mcp.CallToolRequest, in urlInput) (*mcp.CallToolResult, any, error) {
	if !mediautil.Available() {
		return nil, nil, fmt.Errorf("ffprobe is required for stream probing but is not on PATH")
	}
	if err := validateRTSPURL(in.URL); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	info, err := mediautil.ProbeInfo(ctx, in.URL)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSON(info), nil, nil
}

func (s *Server) mcpTakeSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in snapshotInput) (*mcp.CallToolResult, any, error) {
	dev := s.findDevice(in.UUID)
	if dev == nil {
		return nil, nil, fmt.Errorf("no configured device with that uuid")
	}
	data, contentType, err := s.backend.Snapshot(ctx, dev, in.Stream)
	if err != nil {
		return nil, nil, err
	}
	if contentType == "" {
		contentType = "image/jpeg"
	}
	// ImageContent.Data holds the raw bytes; the SDK base64-encodes them on the
	// wire (encoding/json marshals []byte as base64).
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: contentType}},
	}, nil, nil
}

func (s *Server) mcpRunSelfTest(ctx context.Context, _ *mcp.CallToolRequest, in uuidInput) (*mcp.CallToolResult, any, error) {
	dev := s.findDevice(in.UUID)
	if dev == nil {
		return nil, nil, fmt.Errorf("no configured device with that uuid")
	}
	return mcpJSON(s.runONVIFSelfTest(ctx, dev)), nil, nil
}

func (s *Server) mcpGetDiscoveryLog(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	log := s.backend.DiscoveryLog()
	if log == nil {
		log = []discovery.LogEntry{}
	}
	return mcpJSON(log), nil, nil
}

// ---- helpers ---------------------------------------------------------------

// mcpJSON wraps a tool's value in a text-content result: strings pass through
// verbatim, everything else is JSON-serialized. The DTOs marshaled here are all
// plain data, so json.Marshal cannot fail.
func mcpJSON(v any) *mcp.CallToolResult {
	if str, ok := v.(string); ok {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: str}}}
	}
	b, _ := json.Marshal(v)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// validateRTSPURL rejects anything that is not a well-formed rtsp:// URL, so
// these tools cannot be turned into a generic port scanner (mirrors the REST
// probe endpoints).
func validateRTSPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "rtsp" || u.Host == "" {
		return fmt.Errorf("url must be a valid rtsp:// URL")
	}
	return nil
}

// withOriginCheck rejects cross-origin browser requests to defend against DNS
// rebinding (MCP spec MUST). A request with no Origin header is allowed
// (non-browser clients); a present Origin must be loopback or share the request
// Host's hostname, otherwise it is 403.
func withOriginCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !originAllowed(u.Hostname(), r.Host) {
				writeErr(w, http.StatusForbidden, "forbidden", "cross-origin request rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(originHost, reqHost string) bool {
	switch originHost {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	host := reqHost
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		host = h
	}
	return originHost != "" && originHost == host
}
