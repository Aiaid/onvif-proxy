package rtsp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer runs a scripted RTSP responder on a local listener. handler is
// called once per accepted connection with a bufio.Reader over the request
// stream and the raw connection to write responses.
func fakeServer(t *testing.T, handler func(br *bufio.Reader, conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		handler(br, conn)
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// readRequest reads one RTSP request (headers + optional body) from br and
// returns the raw header block plus a parsed header map (lower-cased keys).
func readRequest(t *testing.T, br *bufio.Reader) (firstLine string, headers map[string]string) {
	t.Helper()
	headers = make(map[string]string)
	first, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read request line: %v", err)
	}
	firstLine = strings.TrimRight(first, "\r\n")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if idx := strings.IndexByte(line, ':'); idx >= 0 {
			k := strings.ToLower(strings.TrimSpace(line[:idx]))
			v := strings.TrimSpace(line[idx+1:])
			headers[k] = v
		}
	}
	return firstLine, headers
}

func cseqOf(headers map[string]string) string {
	if v, ok := headers["cseq"]; ok {
		return v
	}
	return "1"
}

func sdpVideoH264() string {
	return "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=Session\r\n" +
		"m=video 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 H264/90000\r\n" +
		"a=fmtp:96 packetization-mode=1; sprop-parameter-sets=Z0IACpZTBYmI,aMljiA==\r\n" +
		"m=audio 0 RTP/AVP 97\r\n" +
		"a=rtpmap:97 PCMU/8000\r\n"
}

func writeResponse(conn net.Conn, cseq, status, body string) {
	var b strings.Builder
	b.WriteString("RTSP/1.0 ")
	b.WriteString(status)
	b.WriteString("\r\n")
	b.WriteString("CSeq: ")
	b.WriteString(cseq)
	b.WriteString("\r\n")
	b.WriteString("Server: FakeRTSP/1.0\r\n")
	if body != "" {
		b.WriteString("Content-Type: application/sdp\r\n")
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	conn.Write([]byte(b.String()))
}

// Scenario 1: OPTIONS 200 + DESCRIBE 200 with H264 SDP -> OK.
func TestProbe_Success(t *testing.T) {
	addr := fakeServer(t, func(br *bufio.Reader, conn net.Conn) {
		_, h := readRequest(t, br) // OPTIONS
		writeResponse(conn, cseqOf(h), "200 OK", "")
		_, h = readRequest(t, br) // DESCRIBE
		writeResponse(conn, cseqOf(h), "200 OK", sdpVideoH264())
	})

	res := Probe(context.Background(), "rtsp://"+addr+"/stream")
	if !res.OK {
		t.Fatalf("expected OK, got errKind=%q detail=%q", res.ErrKind, res.ErrDetail)
	}
	if res.Status != 200 {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if res.Server != "FakeRTSP/1.0" {
		t.Errorf("server = %q", res.Server)
	}
	var video *Track
	for i := range res.Tracks {
		if res.Tracks[i].Type == "video" {
			video = &res.Tracks[i]
		}
	}
	if video == nil {
		t.Fatal("no video track parsed")
	}
	if video.Codec != "H264" {
		t.Errorf("codec = %q, want H264", video.Codec)
	}
	if !strings.Contains(video.Fmtp, "packetization-mode=1") {
		t.Errorf("fmtp = %q", video.Fmtp)
	}
	if res.Auth != "none" {
		t.Errorf("auth = %q, want none", res.Auth)
	}
}

// Scenario 2: DESCRIBE 401 Digest -> client retries with a correct response.
func TestProbe_DigestRetry(t *testing.T) {
	const (
		user  = "admin"
		pass  = "secret"
		realm = "IPCAM"
		nonce = "abc123nonce"
	)
	var gotAuthorization string
	addr := fakeServer(t, func(br *bufio.Reader, conn net.Conn) {
		_, h := readRequest(t, br) // OPTIONS
		writeResponse(conn, cseqOf(h), "200 OK", "")

		_, h = readRequest(t, br) // DESCRIBE (no auth)
		var b strings.Builder
		b.WriteString("RTSP/1.0 401 Unauthorized\r\n")
		b.WriteString("CSeq: " + cseqOf(h) + "\r\n")
		b.WriteString("Server: FakeRTSP/1.0\r\n")
		fmt.Fprintf(&b, "WWW-Authenticate: Digest realm=%q, nonce=%q, qop=\"auth\"\r\n", realm, nonce)
		b.WriteString("\r\n")
		conn.Write([]byte(b.String()))

		first, h := readRequest(t, br) // DESCRIBE (with auth)
		gotAuthorization = h["authorization"]
		// Verify the digest response value.
		params := parseAuthParams(strings.TrimPrefix(gotAuthorization, "Digest"))
		uri := strings.Fields(first)[1]
		want := digestResponse("DESCRIBE", uri, user, realm, pass, nonce, "auth", params["nc"], params["cnonce"])
		if params["response"] != want {
			writeResponse(conn, cseqOf(h), "401 Unauthorized", "")
			return
		}
		writeResponse(conn, cseqOf(h), "200 OK", sdpVideoH264())
	})

	url := fmt.Sprintf("rtsp://%s:%s@%s/stream", user, pass, addr)
	res := Probe(context.Background(), url)
	if !res.OK {
		t.Fatalf("expected OK, got errKind=%q detail=%q auth=%q", res.ErrKind, res.ErrDetail, gotAuthorization)
	}
	if res.Auth != "digest" {
		t.Errorf("auth = %q, want digest", res.Auth)
	}
	if !strings.Contains(gotAuthorization, `qop=auth`) || !strings.Contains(gotAuthorization, "nc=00000001") {
		t.Errorf("authorization header missing qop/nc: %q", gotAuthorization)
	}
}

// Scenario 3: DESCRIBE 404 -> ErrNotFound.
func TestProbe_NotFound(t *testing.T) {
	addr := fakeServer(t, func(br *bufio.Reader, conn net.Conn) {
		_, h := readRequest(t, br)
		writeResponse(conn, cseqOf(h), "200 OK", "")
		_, h = readRequest(t, br)
		writeResponse(conn, cseqOf(h), "404 Not Found", "")
	})

	res := Probe(context.Background(), "rtsp://"+addr+"/missing")
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.ErrKind != ErrNotFound {
		t.Errorf("errKind = %q, want %q", res.ErrKind, ErrNotFound)
	}
	if res.Status != 404 {
		t.Errorf("status = %d, want 404", res.Status)
	}
}

// Scenario 4: SDP with only an audio track -> ErrNoVideoTrack.
func TestProbe_NoVideoTrack(t *testing.T) {
	sdp := "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=Session\r\n" +
		"m=audio 0 RTP/AVP 97\r\n" +
		"a=rtpmap:97 PCMU/8000\r\n"
	addr := fakeServer(t, func(br *bufio.Reader, conn net.Conn) {
		_, h := readRequest(t, br)
		writeResponse(conn, cseqOf(h), "200 OK", "")
		_, h = readRequest(t, br)
		writeResponse(conn, cseqOf(h), "200 OK", sdp)
	})

	res := Probe(context.Background(), "rtsp://"+addr+"/audioonly")
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.ErrKind != ErrNoVideoTrack {
		t.Errorf("errKind = %q, want %q", res.ErrKind, ErrNoVideoTrack)
	}
}

func TestProbe_DialTimeout(t *testing.T) {
	// 203.0.113.0/24 is TEST-NET-3 (RFC 5737), not routable -> dial fails/times out.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	res := Probe(ctx, "rtsp://203.0.113.1:554/stream")
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.ErrKind != ErrDialTimeout {
		t.Errorf("errKind = %q, want %q", res.ErrKind, ErrDialTimeout)
	}
}

// Known RFC 2617 test vector for the MD5 Digest response.
func TestDigestResponse_KnownVector(t *testing.T) {
	got := digestResponse(
		"GET", "/dir/index.html",
		"Mufasa", "testrealm@host.com", "Circle Of Life",
		"dcd98b7102dd2f0e8b11d0f600bfb0c093", "auth", "00000001", "0a4f113b",
	)
	const want = "6629fae49393a05397450978507c4ef1"
	if got != want {
		t.Errorf("digestResponse = %q, want %q", got, want)
	}
}

func TestParseAuthChallenge_Basic(t *testing.T) {
	ch := parseAuthChallenge(`Basic realm="cam"`)
	if ch == nil || ch.scheme != "basic" {
		t.Fatalf("expected basic challenge, got %+v", ch)
	}
}
