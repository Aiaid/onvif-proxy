package mediautil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// grabTimeout bounds a Grab call when the caller's context has no deadline.
const grabTimeout = 15 * time.Second

// stderrTail is how many trailing bytes of ffmpeg stderr are surfaced in errors.
const stderrTail = 300

// waitDelay bounds how long a killed ffmpeg process may keep its pipes open
// before Wait forcibly reaps it, guarding against a hung child.
const waitDelay = 5 * time.Second

// Available reports whether an ffmpeg binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// Grab captures a single JPEG frame from the given RTSP URL using ffmpeg. The
// URL is passed only as an exec argument, never through a shell. When ctx has
// no deadline a 15s timeout is applied. On failure the returned error includes
// a trailing summary of ffmpeg's stderr; a successful run that produces no
// output is also treated as a failure.
func Grab(ctx context.Context, rtspURL string) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, grabTimeout)
		defer cancel()
	}

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-frames:v", "1",
		"-f", "image2",
		"-c:v", "mjpeg",
		"pipe:1",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	// If ctx is cancelled, CommandContext kills the process; WaitDelay ensures
	// Wait does not block forever on pipes held open by a stuck child.
	cmd.WaitDelay = waitDelay

	var stdout bytes.Buffer
	stderr := &tailWriter{max: stderrTail}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg grab failed: %w: %s", err, stderr.summary())
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg grab produced no output: %s", stderr.summary())
	}

	return stdout.Bytes(), nil
}

// tailWriter is an io.Writer that retains only the last max bytes written,
// keeping ffmpeg's most recent (and most relevant) diagnostics without
// unbounded memory growth.
type tailWriter struct {
	max int
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// summary returns the retained stderr tail as a trimmed single-purpose string.
func (t *tailWriter) summary() string {
	s := strings.TrimSpace(string(t.buf))
	if s == "" {
		return "(no stderr output)"
	}
	return s
}
