package mediautil

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// probeTimeout bounds a ProbeInfo call when the caller's context has no deadline.
const probeTimeout = 15 * time.Second

// StreamInfo is the codec/resolution/frame-rate summary of an RTSP video stream,
// extracted by ffprobe. FPS is 0 when the source reports no frame rate.
// Bitrate is in kbps: taken from ffprobe metadata when the source declares it,
// otherwise measured by copying the stream for a few seconds; 0 when unknown.
type StreamInfo struct {
	Codec   string `json:"codec"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Bitrate int    `json:"bitrate"`
}

// ffprobeStream mirrors the ffprobe JSON output for a single stream entry.
type ffprobeStream struct {
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	BitRate      string `json:"bit_rate"`
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
		"-show_entries", "stream=codec_name,width,height,avg_frame_rate,bit_rate",
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
	info := &StreamInfo{
		Codec:  st.CodecName,
		Width:  st.Width,
		Height: st.Height,
		FPS:    parseFrameRate(st.AvgFrameRate),
	}
	// Live RTSP sources rarely declare bit_rate; fall back to measuring the
	// real packet rate. Measurement failure is not an error — bitrate is
	// advisory (capability announcements only) and stays 0 when unknown.
	if br, err := strconv.Atoi(st.BitRate); err == nil && br > 0 {
		info.Bitrate = br / 1000
	} else if br := measureBitrate(ctx, rtspURL); br > 0 {
		info.Bitrate = br
	}
	return info, nil
}

// measureSeconds is how much stream time the bitrate fallback samples.
const measureSeconds = 3

// measureBitrate copies the video stream for measureSeconds without decoding
// and derives kbps from ffmpeg's copied-bytes summary. Returns 0 on any error.
func measureBitrate(ctx context.Context, rtspURL string) int {
	args := []string{
		"-hide_banner",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-map", "0:v:0",
		"-c", "copy",
		"-t", strconv.Itoa(measureSeconds),
		"-f", "null", "-",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.WaitDelay = waitDelay

	stderr := &tailWriter{max: stderrTail}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return 0
	}
	return kbpsFromSummary(stderr.summary(), measureSeconds)
}

// summaryVideoRe matches the copied-bytes summary ffmpeg prints on exit,
// e.g. "video:1131KiB audio:0KiB ..." (older versions print "kB").
var summaryVideoRe = regexp.MustCompile(`video:\s*(\d+)\s*(KiB|kB)`)

// kbpsFromSummary extracts the video byte count from an ffmpeg run summary and
// converts it to kbps over the sampled duration. Returns 0 when absent.
func kbpsFromSummary(summary string, seconds int) int {
	m := summaryVideoRe.FindStringSubmatch(summary)
	if m == nil || seconds <= 0 {
		return 0
	}
	kib, err := strconv.Atoi(m[1])
	if err != nil || kib <= 0 {
		return 0
	}
	return int(math.Round(float64(kib) * 1024 * 8 / float64(seconds) / 1000))
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
