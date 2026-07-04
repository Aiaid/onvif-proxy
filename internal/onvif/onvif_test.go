package onvif

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
)

const testYAML = `
server:
  advertise_ip: 192.168.1.50
web:
  enabled: false
devices:
  - name: Front Door
    ports:
      soap: 8081
      rtsp: 8551
    info:
      manufacturer: TestCorp
      model: TC-1000
      firmware: 9.9.9
    auth:
      username: admin
      password: secret
    streams:
      - name: main
        rtsp: rtsp://user:pass@10.0.0.5:554/ch1/main
        width: 1920
        height: 1080
        framerate: 25
        bitrate: 4096
      - name: sub
        rtsp: rtsp://user:pass@10.0.0.5:554/ch1/sub
        width: 640
        height: 480
        framerate: 15
        bitrate: 512
`

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	dev := cfg.Devices[0]
	dev.UUID = "12345678-1234-1234-1234-1234567890ab"
	dev.MAC = "02:00:00:aa:bb:cc"
	dev.Serial = "12345678"
	return NewServer(dev, Options{AdvertiseIP: "192.168.1.50", Version: "1.0.0"})
}

func soapEnv(header, body string) string {
	return `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:tds="http://www.onvif.org/ver10/device/wsdl"` +
		` xmlns:trt="http://www.onvif.org/ver10/media/wsdl"` +
		` xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">` +
		header + `<s:Body>` + body + `</s:Body></s:Envelope>`
}

func wsseHeader(user, password string) string {
	nonceRaw := []byte("0123456789abcdef")
	created := time.Now().UTC().Format(time.RFC3339)
	h := sha1.New()
	h.Write(nonceRaw)
	h.Write([]byte(created))
	h.Write([]byte(password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return `<s:Header><wsse:Security><wsse:UsernameToken>` +
		`<wsse:Username>` + user + `</wsse:Username>` +
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + digest + `</wsse:Password>` +
		`<wsse:Nonce>` + base64.StdEncoding.EncodeToString(nonceRaw) + `</wsse:Nonce>` +
		`<wsse:Created>` + created + `</wsse:Created>` +
		`</wsse:UsernameToken></wsse:Security></s:Header>`
}

// post drives a SOAP request through the router and returns the recorder.
func post(t *testing.T, s *Server, path, env string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(env))
	req.Host = "cam.local:8081"
	req.Header.Set("Content-Type", "application/soap+xml")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func assertValidXML(t *testing.T, b []byte) {
	t.Helper()
	var v any
	if err := xml.Unmarshal(b, &v); err != nil {
		t.Fatalf("response is not valid XML: %v\n%s", err, b)
	}
}

func TestGetSystemDateAndTimeNoAuth(t *testing.T) {
	s := testServer(t)
	// No WSSE header at all: still allowed because GetSystemDateAndTime is
	// exempt even though the device requires auth.
	rec := post(t, s, "/onvif/device_service", soapEnv("", "<tds:GetSystemDateAndTime/>"))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	if !strings.Contains(rec.Body.String(), "GetSystemDateAndTimeResponse") {
		t.Errorf("missing response element:\n%s", rec.Body)
	}
}

func TestGetCapabilitiesScopesInterfaces(t *testing.T) {
	s := testServer(t)
	cases := map[string]string{
		"GetCapabilities":      "<tds:GetCapabilities/>",
		"GetScopes":            "<tds:GetScopes/>",
		"GetNetworkInterfaces": "<tds:GetNetworkInterfaces/>",
	}
	for name, body := range cases {
		env := soapEnv(wsseHeader("admin", "secret"), body)
		rec := post(t, s, "/onvif/device_service", env)
		if rec.Code != 200 {
			t.Errorf("%s: status = %d, want 200\n%s", name, rec.Code, rec.Body)
			continue
		}
		assertValidXML(t, rec.Body.Bytes())
	}
}

func TestUnknownMethodFault(t *testing.T) {
	s := testServer(t)
	env := soapEnv(wsseHeader("admin", "secret"), "<tds:GetSystemLog/>")
	rec := post(t, s, "/onvif/device_service", env)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	if !strings.Contains(rec.Body.String(), "ActionNotSupported") {
		t.Errorf("subcode missing ActionNotSupported:\n%s", rec.Body)
	}
}

func TestBadWSSEReturns400(t *testing.T) {
	s := testServer(t)
	env := soapEnv(wsseHeader("admin", "wrongpassword"), "<tds:GetDeviceInformation/>")
	rec := post(t, s, "/onvif/device_service", env)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	if !strings.Contains(rec.Body.String(), "NotAuthorized") {
		t.Errorf("subcode missing NotAuthorized:\n%s", rec.Body)
	}
}

func TestGetDeviceInformationValidAuth(t *testing.T) {
	s := testServer(t)
	env := soapEnv(wsseHeader("admin", "secret"), "<tds:GetDeviceInformation/>")
	rec := post(t, s, "/onvif/device_service", env)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	if !strings.Contains(rec.Body.String(), "TestCorp") {
		t.Errorf("manufacturer missing:\n%s", rec.Body)
	}
}

func TestGetProfilesAndStreamUri(t *testing.T) {
	s := testServer(t)

	rec := post(t, s, "/onvif/media_service", soapEnv(wsseHeader("admin", "secret"), "<trt:GetProfiles/>"))
	if rec.Code != 200 {
		t.Fatalf("GetProfiles status = %d\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	if strings.Count(rec.Body.String(), "<trt:Profiles") != 2 {
		t.Errorf("want 2 profiles:\n%s", rec.Body)
	}

	streamReq := "<trt:GetStreamUri><trt:ProfileToken>profile_sub</trt:ProfileToken></trt:GetStreamUri>"
	rec = post(t, s, "/onvif/media_service", soapEnv(wsseHeader("admin", "secret"), streamReq))
	if rec.Code != 200 {
		t.Fatalf("GetStreamUri status = %d\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	// Host comes from the request; port is the proxy port (shared ports.rtsp);
	// credentials must never leak into the advertised URI.
	want := "rtsp://cam.local:8551/ch1/sub"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("stream uri = %s, want %q", rec.Body, want)
	}
	if strings.Contains(rec.Body.String(), "user:pass") {
		t.Errorf("credentials leaked into stream uri:\n%s", rec.Body)
	}
}

func TestGetSnapshotUriAndEndpoint(t *testing.T) {
	s := testServer(t)
	s.opts.SnapshotFunc = func(ctx context.Context, streamName string) ([]byte, string, error) {
		return []byte("JPEGDATA"), "image/jpeg", nil
	}

	req := "<trt:GetSnapshotUri><trt:ProfileToken>profile_main</trt:ProfileToken></trt:GetSnapshotUri>"
	rec := post(t, s, "/onvif/media_service", soapEnv(wsseHeader("admin", "secret"), req))
	if rec.Code != 200 {
		t.Fatalf("GetSnapshotUri status = %d\n%s", rec.Code, rec.Body)
	}
	want := "http://cam.local:8081/onvif/snapshot?token=profile_main"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("snapshot uri missing %q:\n%s", want, rec.Body)
	}

	snap := httptest.NewRequest("GET", "/onvif/snapshot?token=profile_main", nil)
	snapRec := httptest.NewRecorder()
	s.mux.ServeHTTP(snapRec, snap)
	if snapRec.Code != 200 {
		t.Fatalf("snapshot endpoint status = %d", snapRec.Code)
	}
	if snapRec.Body.String() != "JPEGDATA" {
		t.Errorf("snapshot body = %q", snapRec.Body.String())
	}
}

func TestUnknownMediaMethodFault(t *testing.T) {
	s := testServer(t)
	env := soapEnv(wsseHeader("admin", "secret"), "<trt:CreateProfile/>")
	rec := post(t, s, "/onvif/media_service", env)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\n%s", rec.Code, rec.Body)
	}
	assertValidXML(t, rec.Body.Bytes())
	if !strings.Contains(rec.Body.String(), "ActionNotSupported") {
		t.Errorf("missing ActionNotSupported:\n%s", rec.Body)
	}
}
