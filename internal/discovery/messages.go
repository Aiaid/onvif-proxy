package discovery

import (
	crand "crypto/rand"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
)

// WS-Discovery (2005/04 draft, the version ONVIF mandates) constants: SOAP 1.2
// envelope plus the WS-Addressing 2004/08 and Discovery 2005/04 namespaces, and
// the ONVIF network/device WSDL namespaces used in the Types value.
const (
	nsSOAP = "http://www.w3.org/2003/05/soap-envelope"
	nsWSA  = "http://schemas.xmlsoap.org/ws/2004/08/addressing"
	nsDisc = "http://schemas.xmlsoap.org/ws/2005/04/discovery"
	nsNet  = "http://www.onvif.org/ver10/network/wsdl"
	nsDev  = "http://www.onvif.org/ver10/device/wsdl"

	// multicastAddr is the WS-Discovery IPv4 group and port.
	multicastAddr = "239.255.255.250:3702"

	// Well-known WS-Addressing destinations.
	discoveryTo = "urn:schemas-xmlsoap-org:ws:2005:04:discovery"
	anonymousTo = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"

	// WS-Discovery action URIs.
	actionHello        = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Hello"
	actionBye          = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Bye"
	actionProbeMatches = "http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches"

	// deviceTypes is advertised in Hello/Bye/ProbeMatch. Prefixes bind to nsNet
	// (dn) and nsDev (tds) declared on the envelope.
	deviceTypes = "dn:NetworkVideoTransmitter tds:Device"

	// maxDelayMS is APP_MAX_DELAY from the WS-Discovery spec: a ProbeMatch is
	// sent after a random delay in [0, maxDelayMS] to spread out responses.
	maxDelayMS = 500
)

// xmlEscaper escapes the five XML special characters in dynamic text nodes.
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func esc(s string) string { return xmlEscaper.Replace(s) }

// newUUID returns an RFC 4122 version 4 UUID, self-generated from crypto/rand.
func newUUID() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is not recoverable
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newMessageID returns a fresh WS-Addressing MessageID (urn:uuid:<v4>).
func newMessageID() string { return "urn:uuid:" + newUUID() }

// scopesFor builds the space-separated ONVIF scope list for a device. The
// name/hardware segments are URL-escaped so spaces cannot break the list.
func scopesFor(d Device) string {
	scopes := []string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/type/Network_Video_Transmitter",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/name/" + url.PathEscape(d.Name),
		"onvif://www.onvif.org/hardware/" + url.PathEscape(d.Hardware),
		"onvif://www.onvif.org/location/",
	}
	return strings.Join(scopes, " ")
}

// envelope wraps a header and body in the shared SOAP 1.2 envelope with every
// WS-Discovery / ONVIF namespace prefix declared up front.
func envelope(header, body string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<s:Envelope xmlns:s="` + nsSOAP + `"` +
		` xmlns:a="` + nsWSA + `"` +
		` xmlns:d="` + nsDisc + `"` +
		` xmlns:dn="` + nsNet + `"` +
		` xmlns:tds="` + nsDev + `">`)
	b.WriteString(`<s:Header>`)
	b.WriteString(header)
	b.WriteString(`</s:Header>`)
	b.WriteString(`<s:Body>`)
	b.WriteString(body)
	b.WriteString(`</s:Body>`)
	b.WriteString(`</s:Envelope>`)
	return []byte(b.String())
}

// endpointBody is the shared inner payload of Hello, Bye and ProbeMatch: the
// EndpointReference (urn:uuid device address), Types, Scopes, XAddrs and a
// fixed MetadataVersion.
func endpointBody(d Device) string {
	return `<a:EndpointReference><a:Address>urn:uuid:` + esc(d.UUID) + `</a:Address></a:EndpointReference>` +
		`<d:Types>` + deviceTypes + `</d:Types>` +
		`<d:Scopes>` + esc(scopesFor(d)) + `</d:Scopes>` +
		`<d:XAddrs>` + esc(d.XAddr) + `</d:XAddrs>` +
		`<d:MetadataVersion>1</d:MetadataVersion>`
}

// buildHello renders a Hello announcement, multicast on start / device add.
func buildHello(d Device) []byte {
	header := `<a:Action>` + actionHello + `</a:Action>` +
		`<a:MessageID>` + newMessageID() + `</a:MessageID>` +
		`<a:To>` + discoveryTo + `</a:To>`
	return envelope(header, `<d:Hello>`+endpointBody(d)+`</d:Hello>`)
}

// buildBye renders a Bye announcement, multicast on shutdown / device removal.
func buildBye(d Device) []byte {
	header := `<a:Action>` + actionBye + `</a:Action>` +
		`<a:MessageID>` + newMessageID() + `</a:MessageID>` +
		`<a:To>` + discoveryTo + `</a:To>`
	return envelope(header, `<d:Bye>`+endpointBody(d)+`</d:Bye>`)
}

// buildProbeMatches renders a single-device ProbeMatches reply, unicast to the
// prober. RelatesTo echoes the request MessageID; a fresh MessageID is minted.
func buildProbeMatches(d Device, relatesTo string) []byte {
	header := `<a:Action>` + actionProbeMatches + `</a:Action>` +
		`<a:MessageID>` + newMessageID() + `</a:MessageID>` +
		`<a:RelatesTo>` + esc(relatesTo) + `</a:RelatesTo>` +
		`<a:To>` + anonymousTo + `</a:To>`
	body := `<d:ProbeMatches><d:ProbeMatch>` + endpointBody(d) + `</d:ProbeMatch></d:ProbeMatches>`
	return envelope(header, body)
}

// probeDoc is a lightweight view over an incoming envelope. Fields match by
// local name only, so any namespace prefix used by the client is accepted. The
// Probe pointer stays nil unless the Body actually carries a Probe element,
// which distinguishes a Probe from Hello/Bye traffic on the same socket.
type probeDoc struct {
	Header struct {
		MessageID string `xml:"MessageID"`
	} `xml:"Header"`
	Body struct {
		Probe *struct {
			Types string `xml:"Types"`
		} `xml:"Probe"`
	} `xml:"Body"`
}

// parseProbe extracts the MessageID and Types tokens from a datagram. ok is
// false when the message is not a WS-Discovery Probe.
func parseProbe(data []byte) (messageID string, types []string, ok bool) {
	var p probeDoc
	if err := xml.Unmarshal(data, &p); err != nil {
		return "", nil, false
	}
	if p.Body.Probe == nil {
		return "", nil, false
	}
	return strings.TrimSpace(p.Header.MessageID), strings.Fields(p.Body.Probe.Types), true
}

// typesMatch reports whether a Probe with the given Types should be answered:
// an empty Types matches everything, otherwise a token whose local name is
// NetworkVideoTransmitter or Device (prefix irrelevant) is required.
func typesMatch(types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		local := t
		if i := strings.LastIndexByte(t, ':'); i >= 0 {
			local = t[i+1:]
		}
		if local == "NetworkVideoTransmitter" || local == "Device" {
			return true
		}
	}
	return false
}
