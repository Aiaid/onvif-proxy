package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
)

// ONVIF WSDL / WSSE namespaces used when hand-building request envelopes.
const (
	nsSOAP    = "http://www.w3.org/2003/05/soap-envelope"
	nsDevice  = "http://www.onvif.org/ver10/device/wsdl"
	nsMedia   = "http://www.onvif.org/ver10/media/wsdl"
	nsSchema  = "http://www.onvif.org/ver10/schema"
	nsWSSE    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	nsWSU     = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"
	pwDigest  = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest"
	encBase64 = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary"
)

// onvifCheck is one row of the self-test table returned to the UI.
type onvifCheck struct {
	Method     string `json:"method"`
	HTTPStatus int    `json:"http_status"`
	SOAPFault  string `json:"soap_fault"`
	Pass       bool   `json:"pass"`
}

// handleTestONVIF acts as an ONVIF client against the proxy's own SOAP endpoint
// for the given device and reports the outcome of a fixed method matrix.
func (s *Server) handleTestONVIF(w http.ResponseWriter, r *http.Request) {
	dev := s.findDevice(r.URL.Query().Get("device"))
	if dev == nil {
		writeErr(w, http.StatusNotFound, "device not found", "no running device with that uuid")
		return
	}
	checks := s.runONVIFSelfTest(r.Context(), dev)
	writeJSON(w, http.StatusOK, checks)
}

// runONVIFSelfTest exercises the device and media services then a deliberately
// unknown action to confirm Fault well-formedness.
func (s *Server) runONVIFSelfTest(ctx context.Context, dev *config.Device) []onvifCheck {
	deviceURL := fmt.Sprintf("http://127.0.0.1:%d/onvif/device_service", dev.Ports.SOAP)
	mediaURL := fmt.Sprintf("http://127.0.0.1:%d/onvif/media_service", dev.Ports.SOAP)

	profileToken := ""
	if p := dev.PrimaryStream(); p != nil {
		profileToken = p.ProfileToken()
	}

	type call struct {
		method string
		url    string
		ns     string
		body   string
		noAuth bool // GetSystemDateAndTime must succeed without WSSE
	}
	streamSetup := `<tt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream>` +
		`<tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></tt:StreamSetup>`

	calls := []call{
		{"GetSystemDateAndTime", deviceURL, nsDevice, "", true},
		{"GetCapabilities", deviceURL, nsDevice, "<tds:Category>All</tds:Category>", false},
		{"GetServices", deviceURL, nsDevice, "<tds:IncludeCapability>false</tds:IncludeCapability>", false},
		{"GetScopes", deviceURL, nsDevice, "", false},
		{"GetNetworkInterfaces", deviceURL, nsDevice, "", false},
		{"GetDeviceInformation", deviceURL, nsDevice, "", false},
		{"GetProfiles", mediaURL, nsMedia, "", false},
		{"GetStreamUri", mediaURL, nsMedia, streamSetup + "<trt:ProfileToken>" + xmlEscape(profileToken) + "</trt:ProfileToken>", false},
		{"GetSnapshotUri", mediaURL, nsMedia, "<trt:ProfileToken>" + xmlEscape(profileToken) + "</trt:ProfileToken>", false},
	}

	out := make([]onvifCheck, 0, len(calls)+1)
	for _, c := range calls {
		auth := dev.Auth
		if c.noAuth {
			auth = nil
		}
		status, fault, err := s.soapCall(ctx, c.url, c.ns, c.method, c.body, auth)
		chk := onvifCheck{Method: c.method, HTTPStatus: status, SOAPFault: fault}
		chk.Pass = err == nil && status == http.StatusOK && fault == ""
		out = append(out, chk)
	}

	// Deliberately unknown action: expect HTTP 500 + a parseable Fault whose
	// subcode signals ActionNotSupported.
	status, faultText, faultOK, err := s.soapCallFault(ctx, deviceURL, nsDevice, "FooBarNotAMethod", "", dev.Auth)
	chk := onvifCheck{Method: "FooBarNotAMethod", HTTPStatus: status, SOAPFault: faultText}
	chk.Pass = err == nil && status == http.StatusInternalServerError && faultOK &&
		strings.Contains(faultText, "ActionNotSupported")
	out = append(out, chk)

	return out
}

// soapCall performs one method and returns the HTTP status plus any fault text.
func (s *Server) soapCall(ctx context.Context, endpoint, ns, method, inner string, auth *config.Auth) (int, string, error) {
	status, faultText, _, err := s.soapCallFault(ctx, endpoint, ns, method, inner, auth)
	return status, faultText, err
}

// soapCallFault is the full form: it also reports whether the body parsed as a
// SOAP Fault, which the unknown-method probe needs.
func (s *Server) soapCallFault(ctx context.Context, endpoint, ns, method, inner string, auth *config.Auth) (status int, faultText string, isFault bool, err error) {
	envelope := buildEnvelope(ns, method, inner, auth)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(envelope))
	if err != nil {
		return 0, "", false, err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	isFault, faultText = parseFault(body)
	return resp.StatusCode, faultText, isFault, nil
}

// buildEnvelope hand-renders a SOAP 1.2 request. The action element uses a
// service-specific prefix (tds for device, trt for media); tt is used inside
// media parameters. A WSSE UsernameToken header is added when auth is non-nil.
func buildEnvelope(ns, method, inner string, auth *config.Auth) []byte {
	prefix := "tds"
	if ns == nsMedia {
		prefix = "trt"
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<env:Envelope xmlns:env="` + nsSOAP + `"`)
	b.WriteString(` xmlns:tds="` + nsDevice + `"`)
	b.WriteString(` xmlns:trt="` + nsMedia + `"`)
	b.WriteString(` xmlns:tt="` + nsSchema + `">`)
	if auth != nil {
		b.WriteString(wsseHeader(auth.Username, auth.Password))
	}
	b.WriteString(`<env:Body>`)
	if inner == "" {
		b.WriteString(`<` + prefix + `:` + method + ` xmlns:` + prefix + `="` + ns + `"/>`)
	} else {
		b.WriteString(`<` + prefix + `:` + method + ` xmlns:` + prefix + `="` + ns + `">`)
		b.WriteString(inner)
		b.WriteString(`</` + prefix + `:` + method + `>`)
	}
	b.WriteString(`</env:Body></env:Envelope>`)
	return []byte(b.String())
}

// wsseHeader builds a WSSE UsernameToken with a PasswordDigest:
// Digest = Base64(SHA1(nonce + created + password)).
func wsseHeader(username, password string) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	created := time.Now().UTC().Format(time.RFC3339)

	h := sha1.New()
	h.Write(nonce[:])
	h.Write([]byte(created))
	h.Write([]byte(password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	nonceB64 := base64.StdEncoding.EncodeToString(nonce[:])

	var b strings.Builder
	b.WriteString(`<env:Header>`)
	b.WriteString(`<wsse:Security xmlns:wsse="` + nsWSSE + `" xmlns:wsu="` + nsWSU + `">`)
	b.WriteString(`<wsse:UsernameToken>`)
	b.WriteString(`<wsse:Username>` + xmlEscape(username) + `</wsse:Username>`)
	b.WriteString(`<wsse:Password Type="` + pwDigest + `">` + digest + `</wsse:Password>`)
	b.WriteString(`<wsse:Nonce EncodingType="` + encBase64 + `">` + nonceB64 + `</wsse:Nonce>`)
	b.WriteString(`<wsu:Created>` + created + `</wsu:Created>`)
	b.WriteString(`</wsse:UsernameToken></wsse:Security></env:Header>`)
	return b.String()
}

// parseFault reports whether body is a SOAP Fault and extracts a human-readable
// description (the subcode Value if present, else the Reason text).
func parseFault(body []byte) (bool, string) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var (
		hasFault    bool
		inSubcode   bool
		captureText bool
		reason      string
		subcode     string
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Fault":
				hasFault = true
			case "Subcode":
				inSubcode = true
			case "Value":
				if inSubcode {
					captureText = true
				}
			case "Text":
				captureText = true
			}
		case xml.CharData:
			if captureText {
				s := strings.TrimSpace(string(t))
				if s != "" {
					if inSubcode {
						subcode = s
					} else {
						reason = s
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "Subcode":
				inSubcode = false
			case "Value", "Text":
				captureText = false
			}
		}
	}
	if !hasFault {
		return false, ""
	}
	if subcode != "" {
		return true, subcode
	}
	return true, reason
}

// xmlEscape escapes text for safe inclusion in an XML element body.
func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
