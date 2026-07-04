package mediautil

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// probeTimeout bounds a ProbeInfo call when the caller's context has no deadline.
const probeTimeout = 15 * time.Second

// StreamInfo is the codec/resolution/frame-rate summary of an RTSP video stream,
// extracted by ffprobe. FPS is 0 when the source reports no frame rate.
type StreamInfo struct {
	Codec  string `json:"codec"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
}

// ffprobeStream mirrors the ffprobe JSON output for a single stream entry.
type ffprobeStream struct {
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
}

// ProbeInfo runs ffprobe against an RTSP URL and returns the first video
// stream's codec, resolution and frame rate. The URL is passed only as an exec
// argument, never through a shell. When ctx has no deadline a 15s timeout is
// applied. On failure the returned error includes a trailing summary of
// ffprobe's stderr.
func ProbeInfo(ctx context.Context, rtspURL string) (*StreamInfo, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, probeTimeout)
		defer cancel()
	}

	args := []string{
		"-v", "error",
		"-rtsp_transport", "tcp",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,avg_frame_rate",
		"-of", "json",
		rtspURL,
	}

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.WaitDelay = waitDelay

	stderr := &tailWriter{max: stderrTail}
	cmd.Stderr = stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w: %s", err, stderr.summary())
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("ffprobe: parse output: %w", err)
	}
	if len(parsed.Streams) == 0 {
		return nil, fmt.Errorf("ffprobe: no video stream found: %s", stderr.summary())
	}

	st := parsed.Streams[0]
	return &StreamInfo{
		Codec:  st.CodecName,
		Width:  st.Width,
		Height: st.Height,
		FPS:    parseFrameRate(st.AvgFrameRate),
	}, nil
}

// parseFrameRate converts an ffprobe avg_frame_rate ("num/den", e.g. "15/1" or
// "30000/1001") to a rounded integer. A zero or missing denominator yields 0.
func parseFrameRate(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		// Plain integer or float without a denominator.
		if f, err := strconv.ParseFloat(num, 64); err == nil {
			return int(math.Round(f))
		}
		return 0
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return int(math.Round(n / d))
}
