package discovery

import (
	"encoding/xml"
	"strings"
	"testing"
)

// parsedMatch reads back a ProbeMatches / Hello / Bye envelope by local name,
// so the test does not depend on the prefixes chosen when rendering.
type parsedMatch struct {
	Header struct {
		Action    string `xml:"Action"`
		MessageID string `xml:"MessageID"`
		RelatesTo string `xml:"RelatesTo"`
		To        string `xml:"To"`
	} `xml:"Header"`
	Body struct {
		ProbeMatches struct {
			ProbeMatch []endpoint `xml:"ProbeMatch"`
		} `xml:"ProbeMatches"`
		Hello *endpoint `xml:"Hello"`
		Bye   *endpoint `xml:"Bye"`
	} `xml:"Body"`
}

type endpoint struct {
	Address         string `xml:"EndpointReference>Address"`
	Types           string `xml:"Types"`
	Scopes          string `xml:"Scopes"`
	XAddrs          string `xml:"XAddrs"`
	MetadataVersion string `xml:"MetadataVersion"`
}

func sampleDevice() Device {
	return Device{
		UUID:     "12345678-1234-1234-1234-1234567890ab",
		Name:     "Front Door",
		Hardware: "OVP-1000",
		XAddr:    "http://192.168.1.10:8000/onvif/device_service",
	}
}

func assertEndpoint(t *testing.T, ep endpoint, d Device) {
	t.Helper()
	if want := "urn:uuid:" + d.UUID; ep.Address != want {
		t.Errorf("Address = %q, want %q", ep.Address, want)
	}
	if ep.Types != deviceTypes {
		t.Errorf("Types = %q, want %q", ep.Types, deviceTypes)
	}
	if ep.XAddrs != d.XAddr {
		t.Errorf("XAddrs = %q, want %q", ep.XAddrs, d.XAddr)
	}
	if ep.MetadataVersion != "1" {
		t.Errorf("MetadataVersion = %q, want 1", ep.MetadataVersion)
	}
	// Scopes: name/hardware are URL-escaped ("Front Door" -> "Front%20Door").
	if !strings.Contains(ep.Scopes, "onvif://www.onvif.org/name/Front%20Door") {
		t.Errorf("Scopes missing escaped name: %q", ep.Scopes)
	}
	if !strings.Contains(ep.Scopes, "onvif://www.onvif.org/hardware/OVP-1000") {
		t.Errorf("Scopes missing hardware: %q", ep.Scopes)
	}
	for _, fixed := range []string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/type/Network_Video_Transmitter",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/location/",
	} {
		if !strings.Contains(ep.Scopes, fixed) {
			t.Errorf("Scopes missing %q: %q", fixed, ep.Scopes)
		}
	}
}

func TestBuildProbeMatches(t *testing.T) {
	d := sampleDevice()
	const reqID = "urn:uuid:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	var pm parsedMatch
	if err := xml.Unmarshal(buildProbeMatches(d, reqID), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if pm.Header.Action != actionProbeMatches {
		t.Errorf("Action = %q, want %q", pm.Header.Action, actionProbeMatches)
	}
	if pm.Header.RelatesTo != reqID {
		t.Errorf("RelatesTo = %q, want %q", pm.Header.RelatesTo, reqID)
	}
	if pm.Header.To != anonymousTo {
		t.Errorf("To = %q, want %q", pm.Header.To, anonymousTo)
	}
	if !strings.HasPrefix(pm.Header.MessageID, "urn:uuid:") {
		t.Errorf("MessageID = %q, want urn:uuid: prefix", pm.Header.MessageID)
	}
	if pm.Header.MessageID == reqID {
		t.Errorf("response MessageID must differ from request MessageID")
	}
	if n := len(pm.Body.ProbeMatches.ProbeMatch); n != 1 {
		t.Fatalf("ProbeMatch count = %d, want 1", n)
	}
	assertEndpoint(t, pm.Body.ProbeMatches.ProbeMatch[0], d)
}

func TestBuildHello(t *testing.T) {
	d := sampleDevice()
	var pm parsedMatch
	if err := xml.Unmarshal(buildHello(d), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pm.Header.Action != actionHello {
		t.Errorf("Action = %q, want %q", pm.Header.Action, actionHello)
	}
	if pm.Header.To != discoveryTo {
		t.Errorf("To = %q, want %q", pm.Header.To, discoveryTo)
	}
	if pm.Body.Hello == nil {
		t.Fatal("Body has no Hello element")
	}
	assertEndpoint(t, *pm.Body.Hello, d)
}

func TestBuildBye(t *testing.T) {
	d := sampleDevice()
	var pm parsedMatch
	if err := xml.Unmarshal(buildBye(d), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pm.Header.Action != actionBye {
		t.Errorf("Action = %q, want %q", pm.Header.Action, actionBye)
	}
	if pm.Body.Bye == nil {
		t.Fatal("Body has no Bye element")
	}
	assertEndpoint(t, *pm.Body.Bye, d)
}

func TestParseProbe(t *testing.T) {
	// A typical ONVIF client (e.g. ODM / wsdd) Probe: distinct prefixes, a
	// "uuid:"-style MessageID, and Types scoped to NetworkVideoTransmitter.
	const probe = `<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"
            xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <e:Header>
    <w:MessageID>uuid:84ede3de-7dec-11d0-c360-f01234567890</w:MessageID>
    <w:To e:mustUnderstand="true">urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>
    <w:Action e:mustUnderstand="true">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action>
  </e:Header>
  <e:Body>
    <d:Probe>
      <d:Types>dn:NetworkVideoTransmitter</d:Types>
      <d:Scopes/>
    </d:Probe>
  </e:Body>
</e:Envelope>`

	id, types, ok := parseProbe([]byte(probe))
	if !ok {
		t.Fatal("parseProbe ok = false, want true")
	}
	if want := "uuid:84ede3de-7dec-11d0-c360-f01234567890"; id != want {
		t.Errorf("MessageID = %q, want %q", id, want)
	}
	if len(types) != 1 || types[0] != "dn:NetworkVideoTransmitter" {
		t.Errorf("Types = %v, want [dn:NetworkVideoTransmitter]", types)
	}
	if !typesMatch(types) {
		t.Error("typesMatch = false for NetworkVideoTransmitter Probe")
	}
}

func TestParseProbeEmptyTypes(t *testing.T) {
	const probe = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
	                xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
	                xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing">
	  <s:Header><a:MessageID>urn:uuid:1</a:MessageID></s:Header>
	  <s:Body><d:Probe/></s:Body>
	</s:Envelope>`
	_, types, ok := parseProbe([]byte(probe))
	if !ok {
		t.Fatal("parseProbe ok = false, want true")
	}
	if len(types) != 0 {
		t.Errorf("Types = %v, want empty", types)
	}
	if !typesMatch(types) {
		t.Error("empty Types must match (answer all)")
	}
}

func TestParseProbeRejectsNonProbe(t *testing.T) {
	// A Hello arriving on the socket (e.g. our own multicast looped back) must
	// not be treated as a Probe.
	if _, _, ok := parseProbe(buildHello(sampleDevice())); ok {
		t.Error("parseProbe ok = true for Hello, want false")
	}
	if _, _, ok := parseProbe([]byte("not xml at all")); ok {
		t.Error("parseProbe ok = true for garbage, want false")
	}
}

func TestTypesMatch(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  bool
	}{
		{"empty answers all", nil, true},
		{"dn nvt", []string{"dn:NetworkVideoTransmitter"}, true},
		{"tds device", []string{"tds:Device"}, true},
		{"prefix irrelevant", []string{"foo:Device"}, true},
		{"no prefix", []string{"NetworkVideoTransmitter"}, true},
		{"mixed with match", []string{"x:Something", "y:Device"}, true},
		{"unrelated only", []string{"tdn:NetworkVideoDisplay", "tds:Analytics"}, false},
	}
	for _, c := range cases {
		if got := typesMatch(c.types); got != c.want {
			t.Errorf("%s: typesMatch(%v) = %v, want %v", c.name, c.types, got, c.want)
		}
	}
}
