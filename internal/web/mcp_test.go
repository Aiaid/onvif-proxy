package web

// End-to-end tests for the MCP endpoint (docs/07 §3). They drive the real
// /mcp handler over HTTP with the official Go SDK client, exactly as a Claude
// client would: httptest server on s.handler, mcp.NewClient + a
// StreamableClientTransport pointed at srv.URL+"/mcp", then ListTools/CallTool
// on the resulting session.
//
// The fake Backend, testYAML and constants are defined in web_test.go and
// reused here (same package). Only the MCP-specific plumbing lives in this file.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpToolNames is the exact set docs/07 §2 requires, in table order.
var mcpToolNames = []string{
	"get_status",
	"list_devices",
	"get_config",
	"apply_config",
	"add_device",
	"update_device",
	"delete_device",
	"probe_rtsp",
	"get_stream_info",
	"take_snapshot",
	"run_onvif_selftest",
	"get_discovery_log",
}

// newMCPServer builds a Server around the given backend and exposes its full
// handler (with /mcp mounted) over real HTTP.
func newMCPServer(t *testing.T, b Backend) *httptest.Server {
	t.Helper()
	s := New(config.WebConfig{Port: 8080}, b)
	srv := httptest.NewServer(s.handler)
	t.Cleanup(srv.Close)
	return srv
}

// connectMCP opens an SDK client session against srv's /mcp endpoint. The
// standalone SSE stream is disabled: the server runs in stateless mode
// (docs/07 §0), so we only need request/response and want no lingering GET
// stream or reconnect retries during the test.
func connectMCP(t *testing.T, ctx context.Context, srv *httptest.Server) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "onvif-proxy-test", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// testCtx returns a context that bounds each test so a protocol hang fails
// loudly instead of blocking the suite.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// firstText returns the text of the first TextContent block, failing if none.
func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("no text content in result: %+v", res.Content)
	return ""
}

// firstImage returns the first ImageContent block, failing if none.
func firstImage(t *testing.T, res *mcp.CallToolResult) *mcp.ImageContent {
	t.Helper()
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			return ic
		}
	}
	t.Fatalf("no image content in result: %+v", res.Content)
	return nil
}

// 1. Connect performs initialize under the hood; the server must identify
//    itself as "onvif-proxy".
func TestMCPInitializeServerInfo(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	init := session.InitializeResult()
	if init == nil || init.ServerInfo == nil {
		t.Fatalf("missing initialize result / serverInfo: %+v", init)
	}
	if init.ServerInfo.Name != "onvif-proxy" {
		t.Fatalf("serverInfo.name = %q, want onvif-proxy", init.ServerInfo.Name)
	}
}

// 2. tools/list must expose exactly the 12 documented tools, names included.
func TestMCPListTools(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != len(mcpToolNames) {
		var got []string
		for _, tool := range res.Tools {
			got = append(got, tool.Name)
		}
		t.Fatalf("tool count = %d, want %d; got %v", len(res.Tools), len(mcpToolNames), got)
	}
	have := map[string]bool{}
	for _, tool := range res.Tools {
		have[tool.Name] = true
	}
	for _, want := range mcpToolNames {
		if !have[want] {
			t.Fatalf("missing tool %q; have %v", want, have)
		}
	}
}

// 3a. get_status returns the Status JSON as a text block matching the backend.
func TestMCPGetStatus(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_status"})
	if err != nil {
		t.Fatalf("CallTool get_status: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_status reported error: %s", firstText(t, res))
	}
	var st Status
	if err := json.Unmarshal([]byte(firstText(t, res)), &st); err != nil {
		t.Fatalf("decode status json: %v (text=%q)", err, firstText(t, res))
	}
	if st.Version != "test" || st.AdvertiseIP != "10.0.0.1" || !st.FFmpeg {
		t.Fatalf("status mismatch: %+v", st)
	}
}

// 3b. list_devices returns the deviceView array as a text block; the fake
//     backend's single device (uuid + name) must round-trip.
func TestMCPListDevices(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_devices"})
	if err != nil {
		t.Fatalf("CallTool list_devices: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_devices reported error: %s", firstText(t, res))
	}
	var views []deviceView
	if err := json.Unmarshal([]byte(firstText(t, res)), &views); err != nil {
		t.Fatalf("decode devices json: %v (text=%q)", err, firstText(t, res))
	}
	if len(views) != 1 {
		t.Fatalf("device count = %d, want 1: %+v", len(views), views)
	}
	if views[0].Name != "cam1" || views[0].UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("unexpected device: %+v", views[0])
	}
}

// 4. apply_config with dry_run=true, when the backend injects a validation
//    error, surfaces as a tool result with IsError set and the error text in
//    the content (business failure, not a protocol error).
func TestMCPApplyConfigDryRunValidationError(t *testing.T) {
	ctx := testCtx(t)
	b := newTestBackend(t)
	b.applyErr = &config.ValidationError{Problems: []string{"port 8081 used by both web and devices"}}
	session := connectMCP(t, ctx, newMCPServer(t, b))

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "apply_config",
		Arguments: map[string]any{"yaml": testYAML, "dry_run": true},
	})
	if err != nil {
		t.Fatalf("CallTool apply_config returned protocol error, want tool-level isError: %v", err)
	}
	if !res.IsError {
		t.Fatalf("apply_config isError = false, want true: %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), "port 8081") {
		t.Fatalf("error text missing validation detail: %q", firstText(t, res))
	}
	if !b.appliedDry {
		t.Fatalf("apply_config with dry_run=true did not pass dryRun to backend")
	}
}

// 5. delete_device with an unknown uuid is a business failure → IsError.
func TestMCPDeleteDeviceUnknownUUID(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "delete_device",
		Arguments: map[string]any{"uuid": "00000000-0000-0000-0000-000000000000"},
	})
	if err != nil {
		t.Fatalf("CallTool delete_device returned protocol error, want tool-level isError: %v", err)
	}
	if !res.IsError {
		t.Fatalf("delete_device isError = false, want true: %s", firstText(t, res))
	}
}

// 6. Calling a tool that does not exist is an error. The SDK may report this as
//    a protocol-level error (non-nil err) or as an isError result depending on
//    version; either satisfies the contract, so we accept both.
func TestMCPUnknownTool(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "does_not_exist"})
	if err != nil {
		return // protocol-level error: acceptable
	}
	if res == nil || !res.IsError {
		t.Fatalf("unknown tool call did not error: err=%v res=%+v", err, res)
	}
}

// 7. Bare HTTP guards: a plain GET /mcp with no MCP session must not panic and
//    must be rejected (4xx); a POST carrying a foreign Origin must be blocked
//    by the DNS-rebinding guard with 403 (docs/07 §3).
func TestMCPBareHTTPGuards(t *testing.T) {
	srv := newMCPServer(t, newTestBackend(t))

	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET /mcp: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("GET /mcp status = %d, want 4xx", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp with foreign Origin: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /mcp foreign Origin status = %d, want 403", resp2.StatusCode)
	}
}

// 8. take_snapshot returns an image block whose bytes equal the JPEG the fake
//    backend produced. The SDK already base64-decodes on the wire, so
//    ImageContent.Data holds the raw bytes directly.
func TestMCPTakeSnapshot(t *testing.T) {
	ctx := testCtx(t)
	session := connectMCP(t, ctx, newMCPServer(t, newTestBackend(t)))

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "take_snapshot",
		Arguments: map[string]any{"uuid": "11111111-2222-3333-4444-555555555555"},
	})
	if err != nil {
		t.Fatalf("CallTool take_snapshot: %v", err)
	}
	if res.IsError {
		t.Fatalf("take_snapshot reported error: %s", firstText(t, res))
	}
	img := firstImage(t, res)
	if !bytes.Equal(img.Data, []byte{0xff, 0xd8}) {
		t.Fatalf("snapshot bytes = %v, want [0xff 0xd8]", img.Data)
	}
}
