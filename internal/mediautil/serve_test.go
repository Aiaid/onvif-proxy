package mediautil

import (
	"bytes"
	"testing"
)

// fakeJPEG builds a byte slice that begins with an SOI marker, contains the
// given filler payload, and ends with an EOI marker.
func fakeJPEG(payload string) []byte {
	var b bytes.Buffer
	b.Write(jpegSOI)
	b.WriteString(payload)
	b.Write(jpegEOI)
	return b.Bytes()
}

func TestSplitJPEGFramesTwoFrames(t *testing.T) {
	f1 := fakeJPEG("frame-one-data")
	f2 := fakeJPEG("frame-two-data")
	stream := append(append([]byte{}, f1...), f2...)

	frames, rest := splitJPEGFrames(stream)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if !bytes.Equal(frames[0], f1) {
		t.Fatalf("frame 0 = %x, want %x", frames[0], f1)
	}
	if !bytes.Equal(frames[1], f2) {
		t.Fatalf("frame 1 = %x, want %x", frames[1], f2)
	}
	if len(rest) != 0 {
		t.Fatalf("rest = %x, want empty", rest)
	}
}

func TestSplitJPEGFramesLeadingGarbage(t *testing.T) {
	// ffmpeg may emit bytes before the first frame; they must be discarded.
	f1 := fakeJPEG("payload")
	stream := append([]byte("junk-prefix"), f1...)

	frames, rest := splitJPEGFrames(stream)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], f1) {
		t.Fatalf("frame 0 = %x, want %x", frames[0], f1)
	}
	if len(rest) != 0 {
		t.Fatalf("rest = %x, want empty", rest)
	}
}

func TestSplitJPEGFramesPartialTail(t *testing.T) {
	// A complete frame followed by the start of another (no EOI yet). The
	// partial frame must be carried forward as the remainder.
	f1 := fakeJPEG("complete")
	partial := append(append([]byte{}, jpegSOI...), []byte("incomplete...")...)
	stream := append(append([]byte{}, f1...), partial...)

	frames, rest := splitJPEGFrames(stream)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], f1) {
		t.Fatalf("frame 0 = %x, want %x", frames[0], f1)
	}
	if !bytes.Equal(rest, partial) {
		t.Fatalf("rest = %x, want %x", rest, partial)
	}

	// Feeding the rest plus the closing marker should yield the second frame.
	rest = append(rest, jpegEOI...)
	frames2, rest2 := splitJPEGFrames(rest)
	if len(frames2) != 1 {
		t.Fatalf("second pass got %d frames, want 1", len(frames2))
	}
	if len(rest2) != 0 {
		t.Fatalf("second pass rest = %x, want empty", rest2)
	}
}

func TestSplitJPEGFramesTrailingFF(t *testing.T) {
	// A dangling 0xFF might be the first byte of a future SOI and must be kept.
	stream := []byte{0xFF}
	frames, rest := splitJPEGFrames(stream)
	if len(frames) != 0 {
		t.Fatalf("got %d frames, want 0", len(frames))
	}
	if !bytes.Equal(rest, []byte{0xFF}) {
		t.Fatalf("rest = %x, want ff", rest)
	}
}

func TestWriteMJPEGFrame(t *testing.T) {
	var buf bytes.Buffer
	frame := []byte{0xFF, 0xD8, 0x01, 0x02, 0xFF, 0xD9}
	if err := writeMJPEGFrame(&buf, frame); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if !bytes.HasPrefix(out, []byte("--"+mjpegBoundary+"\r\n")) {
		t.Fatalf("part does not start with boundary: %q", out)
	}
	if !bytes.Contains(out, []byte("Content-Type: image/jpeg\r\n")) {
		t.Fatalf("missing content-type header: %q", out)
	}
	if !bytes.Contains(out, []byte("Content-Length: 6\r\n\r\n")) {
		t.Fatalf("missing/incorrect content-length: %q", out)
	}
	if !bytes.HasSuffix(out, append(frame, '\r', '\n')) {
		t.Fatalf("part does not end with frame + CRLF: %q", out)
	}
}
