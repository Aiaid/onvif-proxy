package mediautil

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseFrameRate(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"15/1", 15},
		{"30/1", 30},
		{"30000/1001", 30},
		{"25000/1000", 25},
		{"0/0", 0},
		{"1/0", 0},
		{"", 0},
		{"24", 24},
		{"bogus", 0},
	}
	for _, c := range cases {
		if got := parseFrameRate(c.in); got != c.want {
			t.Errorf("parseFrameRate(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestProbeInfoUnreachable is an integration test requiring a real ffprobe
// binary. There is no RTSP source in the test environment, so it asserts that
// probing an unreachable address fails.
func TestProbeInfoUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ffprobe integration test in short mode")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := ProbeInfo(ctx, "rtsp://127.0.0.1:1/nonexistent")
	if err == nil {
		t.Fatal("expected error probing unreachable RTSP address, got nil")
	}
	if !strings.Contains(err.Error(), "ffprobe") {
		t.Fatalf("error should mention ffprobe, got: %v", err)
	}
}
