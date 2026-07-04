package mediautil

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAvailable(t *testing.T) {
	// This only exercises the LookPath path; the result depends on the host.
	_ = Available()
}

// TestGrabUnreachable is an integration test that requires a real ffmpeg
// binary. It is skipped under `go test -short`. There is no RTSP source in the
// test environment, so it asserts that grabbing from an unreachable address
// fails and that the error carries an ffmpeg stderr summary.
func TestGrabUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ffmpeg integration test in short mode")
	}
	if !Available() {
		t.Skip("ffmpeg not found on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Port 1 is not bound; ffmpeg should fail to connect quickly.
	_, err := Grab(ctx, "rtsp://127.0.0.1:1/nonexistent")
	if err == nil {
		t.Fatal("expected error grabbing from unreachable RTSP address, got nil")
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("error should mention ffmpeg, got: %v", err)
	}
}
