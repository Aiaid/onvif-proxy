package soap

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseRequestActionAndNamespace(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
  <s:Body>
    <trt:GetStreamUri>
      <trt:ProfileToken>profile_main</trt:ProfileToken>
    </trt:GetStreamUri>
  </s:Body>
</s:Envelope>`)

	req, err := ParseRequest(body)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if req.Action != "GetStreamUri" {
		t.Errorf("Action = %q, want GetStreamUri", req.Action)
	}
	if req.Namespace != NSMedia {
		t.Errorf("Namespace = %q, want %q", req.Namespace, NSMedia)
	}
	if got, ok := ExtractElement(req.Body, "ProfileToken"); !ok || got != "profile_main" {
		t.Errorf("ExtractElement ProfileToken = %q, %v", got, ok)
	}
	if req.Security != nil {
		t.Errorf("Security = %+v, want nil", req.Security)
	}
}

func TestParseRequestWithSecurity(t *testing.T) {
	body := []byte(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
  xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
  <s:Header>
    <wsse:Security>
      <wsse:UsernameToken>
        <wsse:Username>admin</wsse:Username>
        <wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">ZGlnZXN0</wsse:Password>
        <wsse:Nonce>bm9uY2U=</wsse:Nonce>
        <wsu:Created xmlns:wsu="http://x">2026-07-04T00:00:00Z</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </s:Header>
  <s:Body><tds:GetDeviceInformation xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/></s:Body>
</s:Envelope>`)

	req, err := ParseRequest(body)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if req.Action != "GetDeviceInformation" {
		t.Errorf("Action = %q", req.Action)
	}
	if req.Security == nil {
		t.Fatal("Security = nil")
	}
	if req.Security.Username != "admin" || req.Security.Nonce != "bm9uY2U=" {
		t.Errorf("token = %+v", req.Security)
	}
	if !strings.Contains(req.Security.PasswordType, "PasswordDigest") {
		t.Errorf("PasswordType = %q", req.Security.PasswordType)
	}
}

func TestVerifyPasswordDigest(t *testing.T) {
	const user, pass = "admin", "secret"
	nonceRaw := []byte("0123456789abcdef")
	created := time.Now().UTC().Format(time.RFC3339)
	nonceB64 := base64.StdEncoding.EncodeToString(nonceRaw)

	h := sha1.New()
	h.Write(nonceRaw)
	h.Write([]byte(created))
	h.Write([]byte(pass))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))

	tok := &UsernameToken{
		Username:     user,
		Password:     digest,
		PasswordType: "...#PasswordDigest",
		Nonce:        nonceB64,
		Created:      created,
	}
	if !tok.Verify(user, pass, 5*time.Minute) {
		t.Error("valid digest rejected")
	}
	if tok.Verify(user, "wrong", 5*time.Minute) {
		t.Error("wrong password accepted")
	}
	if tok.Verify("other", pass, 5*time.Minute) {
		t.Error("wrong username accepted")
	}

	// Created outside the skew window must fail.
	old := *tok
	oldCreated := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	h2 := sha1.New()
	h2.Write(nonceRaw)
	h2.Write([]byte(oldCreated))
	h2.Write([]byte(pass))
	old.Created = oldCreated
	old.Password = base64.StdEncoding.EncodeToString(h2.Sum(nil))
	if old.Verify(user, pass, 5*time.Minute) {
		t.Error("stale Created accepted")
	}
}

func TestVerifyPasswordText(t *testing.T) {
	tok := &UsernameToken{
		Username:     "admin",
		Password:     "secret",
		PasswordType: "...#PasswordText",
	}
	if !tok.Verify("admin", "secret", 5*time.Minute) {
		t.Error("valid text password rejected")
	}
	if tok.Verify("admin", "nope", 5*time.Minute) {
		t.Error("wrong text password accepted")
	}
}

func TestEnvelopeParses(t *testing.T) {
	env := Envelope(`<tds:GetHostnameResponse><tds:HostnameInformation><tt:Name>cam</tt:Name></tds:HostnameInformation></tds:GetHostnameResponse>`)
	var v any
	if err := xml.Unmarshal(env, &v); err != nil {
		t.Fatalf("rendered envelope is not valid XML: %v", err)
	}
}

// faultDoc mirrors a SOAP 1.2 Fault for reflective verification.
type faultDoc struct {
	XMLName xml.Name
	Body    struct {
		Fault struct {
			Code struct {
				Value   string `xml:"Value"`
				Subcode struct {
					Value string `xml:"Value"`
				} `xml:"Subcode"`
			} `xml:"Code"`
			Reason struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

func TestWriteActionNotSupportedFault(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteActionNotSupported(rec, "GetSystemLog")

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q", ct)
	}

	var doc faultDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("fault not valid XML: %v", err)
	}
	if !strings.HasSuffix(doc.Body.Fault.Code.Value, "Receiver") {
		t.Errorf("Code.Value = %q", doc.Body.Fault.Code.Value)
	}
	if !strings.HasSuffix(doc.Body.Fault.Code.Subcode.Value, "ActionNotSupported") {
		t.Errorf("Subcode.Value = %q", doc.Body.Fault.Code.Subcode.Value)
	}
	if !strings.Contains(doc.Body.Fault.Reason.Text, "GetSystemLog") {
		t.Errorf("Reason = %q", doc.Body.Fault.Reason.Text)
	}
}

func TestWriteNotAuthorizedFault(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteNotAuthorized(rec)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var doc faultDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("fault not valid XML: %v", err)
	}
	if !strings.HasSuffix(doc.Body.Fault.Code.Value, "Sender") {
		t.Errorf("Code.Value = %q", doc.Body.Fault.Code.Value)
	}
	if !strings.HasSuffix(doc.Body.Fault.Code.Subcode.Value, "NotAuthorized") {
		t.Errorf("Subcode.Value = %q", doc.Body.Fault.Code.Subcode.Value)
	}
}

func TestXMLEscape(t *testing.T) {
	got := XMLEscape(`a & b <c> "d"`)
	if strings.Contains(got, "<c>") || !strings.Contains(got, "&amp;") {
		t.Errorf("XMLEscape = %q", got)
	}
}
