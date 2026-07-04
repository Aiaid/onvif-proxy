// Package rtsp implements a minimal native RTSP 1.0 (RFC 2326) probe client
// used by the web UI "test connection" feature. It performs OPTIONS then
// DESCRIBE, handles 401 challenges (Digest per RFC 2617/7616 and Basic), and
// parses the returned SDP (RFC 4566) to report the media tracks.
//
// It intentionally depends only on the standard library.
package rtsp

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrKind classifies why a probe failed.
type ErrKind string

const (
	ErrDialTimeout  ErrKind = "dial_timeout"
	ErrAuthFailed   ErrKind = "auth_failed"
	ErrNotFound     ErrKind = "not_found"
	ErrNoVideoTrack ErrKind = "no_video_track"
	ErrProtocol     ErrKind = "protocol_error"
)

// Track is a single media track parsed from the SDP.
type Track struct {
	Type  string // "video" / "audio"
	Codec string // encoding name, e.g. "H264", "H265", "MPEG4-GENERIC", "JPEG"
	Fmtp  string // raw a=fmtp attribute value (truncated to 200 chars)
}

// Result is the structured outcome of a probe.
type Result struct {
	OK        bool
	Status    int    // final RTSP status code
	Auth      string // "none" / "basic" / "digest"
	Server    string // Server header
	LatencyMS int64
	Tracks    []Track
	ErrKind   ErrKind // valid when OK == false
	ErrDetail string
}

const (
	userAgent    = "onvif-proxy"
	defaultRTSP  = "554"
	dialTimeout  = 5 * time.Second
	totalTimeout = 5 * time.Second
	maxFmtpLen   = 200
)

// Probe connects to rawURL, runs OPTIONS then DESCRIBE (retrying once with
// credentials on a 401), and returns a structured Result. The overall
// operation is bounded to 5s and can be cancelled earlier via ctx.
func Probe(ctx context.Context, rawURL string) *Result {
	res := &Result{Auth: "none"}

	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		res.ErrKind = ErrProtocol
		res.ErrDetail = "invalid RTSP URL"
		return res
	}

	// Bound the whole probe to totalTimeout, still honouring an earlier ctx.
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, defaultRTSP)
	}

	start := time.Now()

	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		res.ErrKind = ErrDialTimeout
		res.ErrDetail = err.Error()
		return res
	}
	defer conn.Close()

	// Close the connection when ctx is cancelled so blocking reads unblock.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}

	br := bufio.NewReader(conn)

	// Credentials come from the URL userinfo.
	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	// Request-URI must not carry the userinfo.
	reqURL := *u
	reqURL.User = nil
	requestURI := reqURL.String()

	cseq := 1

	// Step 1: OPTIONS (best-effort; a failure here surfaces as protocol error).
	if err := writeRequest(conn, "OPTIONS", requestURI, cseq, nil); err != nil {
		return failWrite(res, ctx, err)
	}
	optResp, err := readResponse(br)
	if err != nil {
		return failRead(res, ctx, err)
	}
	if optResp.server != "" {
		res.Server = optResp.server
	}
	cseq++

	// Step 2: DESCRIBE.
	describeHeaders := map[string]string{"Accept": "application/sdp"}
	if err := writeRequest(conn, "DESCRIBE", requestURI, cseq, describeHeaders); err != nil {
		return failWrite(res, ctx, err)
	}
	resp, err := readResponse(br)
	if err != nil {
		return failRead(res, ctx, err)
	}
	cseq++

	// Step 3: handle 401 with a single authenticated retry.
	if resp.status == 401 {
		ch := parseAuthChallenge(resp.headers["www-authenticate"])
		if ch == nil {
			res.Status = resp.status
			res.ErrKind = ErrAuthFailed
			res.ErrDetail = "401 without a usable WWW-Authenticate header"
			return res
		}

		authHeader, scheme := buildAuthorization(ch, "DESCRIBE", requestURI, username, password)
		res.Auth = scheme
		retryHeaders := map[string]string{
			"Accept":        "application/sdp",
			"Authorization": authHeader,
		}
		if err := writeRequest(conn, "DESCRIBE", requestURI, cseq, retryHeaders); err != nil {
			return failWrite(res, ctx, err)
		}
		resp, err = readResponse(br)
		if err != nil {
			return failRead(res, ctx, err)
		}
		cseq++

		if resp.status == 401 {
			res.Status = resp.status
			res.ErrKind = ErrAuthFailed
			// Surface everything useful for diagnosing a rejected login: which
			// scheme/algorithm the server demanded and whether credentials were
			// present at all. Special characters in the URL userinfo must be
			// percent-encoded, a frequent cause of "wrong password" reports.
			detail := fmt.Sprintf("authentication rejected (scheme=%s", scheme)
			if ch.realm != "" {
				detail += fmt.Sprintf(", realm=%q", ch.realm)
			}
			if ch.algorithm != "" && !strings.EqualFold(ch.algorithm, "MD5") {
				detail += fmt.Sprintf(", algorithm=%s — 本探测器仅支持 MD5", ch.algorithm)
			}
			if username == "" {
				detail += ", URL 未携带账密"
			}
			detail += ");密码含 @ : / % # 等特殊字符时需 URL 编码(如 @ 写作 %40)"
			res.ErrDetail = detail
			return res
		}
	}

	if resp.server != "" {
		res.Server = resp.server
	}
	res.Status = resp.status

	switch {
	case resp.status == 404 || resp.status == 454:
		res.ErrKind = ErrNotFound
		res.ErrDetail = fmt.Sprintf("DESCRIBE returned %d", resp.status)
		return res
	case resp.status != 200:
		res.ErrKind = ErrProtocol
		res.ErrDetail = fmt.Sprintf("unexpected DESCRIBE status %d", resp.status)
		return res
	}

	tracks := parseSDP(resp.body)
	res.Tracks = tracks
	res.LatencyMS = time.Since(start).Milliseconds()

	hasVideo := false
	for _, t := range tracks {
		if t.Type == "video" {
			hasVideo = true
			break
		}
	}
	if !hasVideo {
		res.ErrKind = ErrNoVideoTrack
		res.ErrDetail = "SDP contains no m=video track"
		return res
	}

	res.OK = true
	return res
}

// failWrite/failRead classify a transport error, preferring ctx cancellation.
func failWrite(res *Result, ctx context.Context, err error) *Result {
	return classifyTransport(res, ctx, err)
}

func failRead(res *Result, ctx context.Context, err error) *Result {
	return classifyTransport(res, ctx, err)
}

func classifyTransport(res *Result, ctx context.Context, err error) *Result {
	if ctx.Err() != nil {
		res.ErrKind = ErrDialTimeout
		res.ErrDetail = ctx.Err().Error()
		return res
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		res.ErrKind = ErrDialTimeout
		res.ErrDetail = err.Error()
		return res
	}
	res.ErrKind = ErrProtocol
	res.ErrDetail = err.Error()
	return res
}

// writeRequest emits a single RTSP request line + headers.
func writeRequest(w io.Writer, method, uri string, cseq int, headers map[string]string) error {
	var b strings.Builder
	b.WriteString(method)
	b.WriteString(" ")
	b.WriteString(uri)
	b.WriteString(" RTSP/1.0\r\n")
	b.WriteString("CSeq: ")
	b.WriteString(strconv.Itoa(cseq))
	b.WriteString("\r\n")
	b.WriteString("User-Agent: ")
	b.WriteString(userAgent)
	b.WriteString("\r\n")
	for k, v := range headers {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// response is a parsed RTSP response.
type response struct {
	status  int
	headers map[string]string // lower-cased keys
	server  string
	body    string
}

// readResponse reads a status line, CRLF headers and a Content-Length body.
func readResponse(br *bufio.Reader) (*response, error) {
	statusLine, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	statusLine = strings.TrimRight(statusLine, "\r\n")
	// Expected: RTSP/1.0 200 OK
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "RTSP/") {
		return nil, fmt.Errorf("malformed status line: %q", statusLine)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("malformed status code: %q", parts[1])
	}

	resp := &response{status: code, headers: make(map[string]string)}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		resp.headers[key] = val
	}
	resp.server = resp.headers["server"]

	if cl, ok := resp.headers["content-length"]; ok {
		n, err := strconv.Atoi(cl)
		if err != nil {
			return nil, fmt.Errorf("malformed Content-Length: %q", cl)
		}
		if n > 0 {
			buf := make([]byte, n)
			if _, err := io.ReadFull(br, buf); err != nil {
				return nil, err
			}
			resp.body = string(buf)
		}
	}
	return resp, nil
}

// challenge holds parsed WWW-Authenticate parameters.
type challenge struct {
	scheme    string // "digest" / "basic"
	realm     string
	nonce     string
	qop       string // "" or "auth"
	opaque    string
	algorithm string
}

// parseAuthChallenge parses a WWW-Authenticate header value. It prefers Digest
// when both schemes are offered.
func parseAuthChallenge(header string) *challenge {
	if header == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(header))
	switch {
	case strings.HasPrefix(lower, "digest"):
		ch := &challenge{scheme: "digest"}
		params := parseAuthParams(header[len("Digest"):])
		ch.realm = params["realm"]
		ch.nonce = params["nonce"]
		ch.opaque = params["opaque"]
		ch.algorithm = params["algorithm"]
		if qop, ok := params["qop"]; ok {
			// qop may be a comma-separated list; pick "auth" if present.
			for _, q := range strings.Split(qop, ",") {
				if strings.TrimSpace(q) == "auth" {
					ch.qop = "auth"
					break
				}
			}
		}
		if ch.nonce == "" {
			return nil
		}
		return ch
	case strings.HasPrefix(lower, "basic"):
		return &challenge{scheme: "basic"}
	default:
		return nil
	}
}

// parseAuthParams parses key=value or key="value" pairs from a challenge tail.
func parseAuthParams(s string) map[string]string {
	out := make(map[string]string)
	i := 0
	n := len(s)
	for i < n {
		// Skip separators/whitespace.
		for i < n && (s[i] == ',' || s[i] == ' ' || s[i] == '\t') {
			i++
		}
		// Read key.
		keyStart := i
		for i < n && s[i] != '=' {
			i++
		}
		if i >= n {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[keyStart:i]))
		i++ // skip '='
		if i >= n {
			break
		}
		var val string
		if s[i] == '"' {
			i++
			valStart := i
			for i < n && s[i] != '"' {
				i++
			}
			val = s[valStart:i]
			if i < n {
				i++ // skip closing quote
			}
		} else {
			valStart := i
			for i < n && s[i] != ',' {
				i++
			}
			val = strings.TrimSpace(s[valStart:i])
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// buildAuthorization produces an Authorization header value and the scheme name.
func buildAuthorization(ch *challenge, method, uri, username, password string) (string, string) {
	if ch.scheme == "basic" {
		cred := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		return "Basic " + cred, "basic"
	}
	// Digest.
	cnonce := ""
	nc := ""
	if ch.qop == "auth" {
		cnonce = newCnonce()
		nc = "00000001"
	}
	response := digestResponse(method, uri, username, ch.realm, password, ch.nonce, ch.qop, nc, cnonce)

	var b strings.Builder
	b.WriteString(`Digest username="`)
	b.WriteString(username)
	b.WriteString(`", realm="`)
	b.WriteString(ch.realm)
	b.WriteString(`", nonce="`)
	b.WriteString(ch.nonce)
	b.WriteString(`", uri="`)
	b.WriteString(uri)
	b.WriteString(`", response="`)
	b.WriteString(response)
	b.WriteString(`"`)
	if ch.algorithm != "" {
		b.WriteString(`, algorithm=`)
		b.WriteString(ch.algorithm)
	}
	if ch.qop == "auth" {
		b.WriteString(`, qop=auth, nc=`)
		b.WriteString(nc)
		b.WriteString(`, cnonce="`)
		b.WriteString(cnonce)
		b.WriteString(`"`)
	}
	if ch.opaque != "" {
		b.WriteString(`, opaque="`)
		b.WriteString(ch.opaque)
		b.WriteString(`"`)
	}
	return b.String(), "digest"
}

// digestResponse computes the RFC 2617/7616 MD5 Digest response value.
func digestResponse(method, uri, username, realm, password, nonce, qop, nc, cnonce string) string {
	ha1 := md5hex(username + ":" + realm + ":" + password)
	ha2 := md5hex(method + ":" + uri)
	if qop == "auth" || qop == "auth-int" {
		return md5hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
	}
	return md5hex(ha1 + ":" + nonce + ":" + ha2)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newCnonce() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to a fixed value; determinism is acceptable for a probe.
		return "0a4f113b0a4f113b"
	}
	return hex.EncodeToString(buf)
}

// parseSDP extracts media tracks from an SDP document (RFC 4566). Each m= line
// starts a new track; a=rtpmap supplies the codec name and a=fmtp is captured
// verbatim (truncated).
func parseSDP(sdp string) []Track {
	var tracks []Track
	cur := -1
	// payloadType -> track index, to attach rtpmap/fmtp lines.
	for _, raw := range strings.Split(sdp, "\n") {
		line := strings.TrimRight(raw, "\r")
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		typ := line[0]
		val := line[2:]
		switch typ {
		case 'm':
			// m=<media> <port> <proto> <fmt> ...
			fields := strings.Fields(val)
			if len(fields) == 0 {
				continue
			}
			media := fields[0]
			if media != "video" && media != "audio" {
				cur = -1
				continue
			}
			tracks = append(tracks, Track{Type: media})
			cur = len(tracks) - 1
		case 'a':
			if cur < 0 {
				continue
			}
			switch {
			case strings.HasPrefix(val, "rtpmap:"):
				// a=rtpmap:<pt> <encoding>/<clock>[/<params>]
				rest := strings.TrimPrefix(val, "rtpmap:")
				sp := strings.IndexByte(rest, ' ')
				if sp < 0 {
					continue
				}
				enc := rest[sp+1:]
				if slash := strings.IndexByte(enc, '/'); slash >= 0 {
					enc = enc[:slash]
				}
				if tracks[cur].Codec == "" {
					tracks[cur].Codec = enc
				}
			case strings.HasPrefix(val, "fmtp:"):
				rest := strings.TrimPrefix(val, "fmtp:")
				sp := strings.IndexByte(rest, ' ')
				fmtp := rest
				if sp >= 0 {
					fmtp = rest[sp+1:]
				}
				if len(fmtp) > maxFmtpLen {
					fmtp = fmtp[:maxFmtpLen]
				}
				if tracks[cur].Fmtp == "" {
					tracks[cur].Fmtp = fmtp
				}
			}
		}
	}
	return tracks
}
