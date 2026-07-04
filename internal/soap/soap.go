// Package soap implements the minimal SOAP 1.2 layer shared by the ONVIF
// Device and Media services: envelope parsing via an encoding/xml token
// stream, WS-Security UsernameToken verification (PasswordDigest and
// PasswordText), a single envelope renderer with fixed namespace prefixes,
// and spec-compliant SOAP 1.2 Fault rendering with the ONVIF HTTP status
// mapping.
package soap

import (
	"bytes"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Namespace URIs declared on every rendered envelope.
const (
	NSEnvelope = "http://www.w3.org/2003/05/soap-envelope"
	NSDevice   = "http://www.onvif.org/ver10/device/wsdl"
	NSMedia    = "http://www.onvif.org/ver10/media/wsdl"
	NSSchema   = "http://www.onvif.org/ver10/schema"
	NSError    = "http://www.onvif.org/ver10/error"
)

// ContentType is the SOAP 1.2 media type used for every response.
const ContentType = "application/soap+xml; charset=utf-8"

// Request is a parsed SOAP request. Action is the local name of the first
// child element of the Body (the ONVIF method), Namespace is that element's
// namespace URI, Body is the raw innerxml of the Body (for parameter
// extraction) and Security is the WSSE UsernameToken when present.
type Request struct {
	Action    string
	Namespace string
	Body      []byte
	Security  *UsernameToken
}

// UsernameToken is a WS-Security UsernameToken as sent by ONVIF clients.
type UsernameToken struct {
	Username     string
	Password     string
	PasswordType string
	Nonce        string
	Created      string
}

// wire mirrors the subset of the SOAP envelope we care about. Fields match by
// local name, so any namespace prefix on the wire is accepted.
type wire struct {
	Header struct {
		Security struct {
			UsernameToken *struct {
				Username string `xml:"Username"`
				Password struct {
					Type  string `xml:"Type,attr"`
					Value string `xml:",chardata"`
				} `xml:"Password"`
				Nonce   string `xml:"Nonce"`
				Created string `xml:"Created"`
			} `xml:"UsernameToken"`
		} `xml:"Security"`
	} `xml:"Header"`
	Body struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"Body"`
}

// ParseRequest decodes the envelope, extracts the WSSE token (if any) and
// locates the Body's first child element to determine the action.
func ParseRequest(body []byte) (*Request, error) {
	var w wire
	if err := xml.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("soap: parse envelope: %w", err)
	}

	req := &Request{Body: w.Body.Inner}
	// The Body innerxml alone loses the namespace declarations inherited from
	// the Envelope, so resolve the action element over the full document where
	// the decoder can bind prefixes to their namespace URIs.
	req.Action, req.Namespace = bodyChild(body)

	if ut := w.Header.Security.UsernameToken; ut != nil {
		req.Security = &UsernameToken{
			Username:     ut.Username,
			Password:     ut.Password.Value,
			PasswordType: ut.Password.Type,
			Nonce:        ut.Nonce,
			Created:      ut.Created,
		}
	}
	return req, nil
}

// bodyChild returns the local name and namespace URI of the first child
// element of the SOAP Body, resolving prefixes against the whole document.
func bodyChild(body []byte) (action, namespace string) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	depth := 0
	inBody := false
	bodyDepth := -1
	for {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch se := tok.(type) {
		case xml.StartElement:
			if !inBody && se.Name.Local == "Body" && se.Name.Space == NSEnvelope {
				inBody = true
				bodyDepth = depth
			} else if inBody && depth == bodyDepth+1 {
				return se.Name.Local, se.Name.Space
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

// ExtractElement returns the character data of the first element whose local
// name matches, scanning inner as a token stream.
func ExtractElement(inner []byte, localName string) (string, bool) {
	dec := xml.NewDecoder(bytes.NewReader(inner))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == localName {
			var text string
			if err := dec.DecodeElement(&text, &se); err != nil {
				return "", false
			}
			return strings.TrimSpace(text), true
		}
	}
}

// Verify checks the token against the expected credentials. PasswordDigest is
// verified per the OASIS UsernameToken profile:
//
//	Digest = Base64( SHA-1( Base64Decode(Nonce) + Created + Password ) )
//
// PasswordText is accepted as a plain-text comparison. When a Created
// timestamp is present and parseable it must fall within clockSkew of now.
func (t *UsernameToken) Verify(username, password string, clockSkew time.Duration) bool {
	if t == nil || t.Username != username {
		return false
	}
	if t.Created != "" {
		if created, ok := parseCreated(t.Created); ok {
			if d := time.Since(created); d > clockSkew || d < -clockSkew {
				return false
			}
		}
	}
	if isDigest(t.PasswordType) || (t.PasswordType == "" && t.Nonce != "") {
		nonce, err := base64.StdEncoding.DecodeString(t.Nonce)
		if err != nil {
			return false
		}
		h := sha1.New()
		h.Write(nonce)
		h.Write([]byte(t.Created))
		h.Write([]byte(password))
		want := base64.StdEncoding.EncodeToString(h.Sum(nil))
		return subtle.ConstantTimeCompare([]byte(want), []byte(t.Password)) == 1
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(t.Password)) == 1
}

func isDigest(passwordType string) bool {
	return strings.Contains(passwordType, "PasswordDigest")
}

func parseCreated(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

const envelopeHeader = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<env:Envelope` +
	` xmlns:env="` + NSEnvelope + `"` +
	` xmlns:tds="` + NSDevice + `"` +
	` xmlns:trt="` + NSMedia + `"` +
	` xmlns:tt="` + NSSchema + `"` +
	` xmlns:ter="` + NSError + `">` +
	`<env:Body>`

const envelopeFooter = `</env:Body></env:Envelope>`

// Envelope wraps an already-rendered Body payload in a SOAP 1.2 envelope with
// the fixed ONVIF namespace prefixes declared on the Envelope element.
func Envelope(body string) []byte {
	var b bytes.Buffer
	b.Grow(len(envelopeHeader) + len(body) + len(envelopeFooter))
	b.WriteString(envelopeHeader)
	b.WriteString(body)
	b.WriteString(envelopeFooter)
	return b.Bytes()
}

// WriteFault renders a SOAP 1.2 Fault. code is "Sender" or "Receiver"
// (rendered as env:Sender / env:Receiver); subcode is a prefixed QName such as
// "ter:ActionNotSupported". The HTTP status follows the SOAP 1.2 mapping:
// Receiver -> 500, Sender -> 400.
func WriteFault(w http.ResponseWriter, code, subcode, reason string) {
	body := `<env:Fault>` +
		`<env:Code><env:Value>env:` + code + `</env:Value>` +
		`<env:Subcode><env:Value>` + subcode + `</env:Value></env:Subcode>` +
		`</env:Code>` +
		`<env:Reason><env:Text xml:lang="en">` + XMLEscape(reason) + `</env:Text></env:Reason>` +
		`</env:Fault>`

	status := http.StatusBadRequest
	if code == "Receiver" {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	w.Write(Envelope(body))
}

// WriteActionNotSupported reports an unimplemented (optional) method as an
// env:Receiver / ter:ActionNotSupported fault with HTTP 500.
func WriteActionNotSupported(w http.ResponseWriter, action string) {
	WriteFault(w, "Receiver", "ter:ActionNotSupported",
		"The requested action is not supported: "+action)
}

// WriteNotAuthorized reports failed or missing authentication as an
// env:Sender / ter:NotAuthorized fault with HTTP 400.
func WriteNotAuthorized(w http.ResponseWriter) {
	WriteFault(w, "Sender", "ter:NotAuthorized", "Sender not authorized")
}

// WriteInvalidArg reports a malformed request or invalid argument as an
// env:Sender / ter:InvalidArgVal fault with HTTP 400.
func WriteInvalidArg(w http.ResponseWriter, reason string) {
	WriteFault(w, "Sender", "ter:InvalidArgVal", reason)
}

// XMLEscape escapes a string for inclusion in XML character data or an
// attribute value.
func XMLEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
