package mediautil

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os/exec"
)

// mjpegBoundary is the multipart boundary used for the self-framed MJPEG stream.
const mjpegBoundary = "ffmpeg"

// readChunk is the read buffer size when draining ffmpeg's stdout.
const readChunk = 32 * 1024

// JPEG start-of-image and end-of-image markers.
var (
	jpegSOI = []byte{0xFF, 0xD8}
	jpegEOI = []byte{0xFF, 0xD9}
)

// ServeMJPEG pulls the RTSP stream with ffmpeg, scales it to at most maxWidth,
// throttles to fps frames per second, drops audio, and streams the frames to w
// as a multipart/x-mixed-replace response that an <img> tag can render live.
//
// ffmpeg emits a raw concatenated MJPEG stream on stdout; this function splits
// it into individual JPEG frames (SOI..EOI) and re-wraps each in its own
// multipart part, which is more robust than trusting a muxer's boundary format.
// When the client disconnects (r.Context cancelled) the ffmpeg process is
// killed and the function returns.
func ServeMJPEG(w http.ResponseWriter, r *http.Request, rtspURL string, maxWidth, fps int) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("mediautil: ResponseWriter does not support flushing")
	}

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-an",
		// The comma inside min() is escaped so ffmpeg's filtergraph parser does
		// not read it as a filter separator. -2 keeps the height even.
		"-vf", fmt.Sprintf("scale=min(%d\\,iw):-2", maxWidth),
		"-r", fmt.Sprintf("%d", fps),
		"-q:v", "8",
		"-c:v", "mjpeg",
		"-f", "mjpeg",
		"pipe:1",
	}

	// r.Context() is cancelled when the client disconnects; CommandContext then
	// kills ffmpeg, and WaitDelay guards against a stuck child.
	cmd := exec.CommandContext(r.Context(), "ffmpeg", args...)
	cmd.WaitDelay = waitDelay

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mediautil: stdout pipe: %w", err)
	}
	stderr := &tailWriter{max: stderrTail}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mediautil: start ffmpeg: %w", err)
	}
	// Ensure the process is always reaped.
	defer func() { _ = cmd.Wait() }()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+mjpegBoundary)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "close")

	buf := make([]byte, 0, readChunk)
	chunk := make([]byte, readChunk)
	for {
		n, readErr := stdout.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			var frames [][]byte
			frames, buf = splitJPEGFrames(buf)
			for _, frame := range frames {
				if err := writeMJPEGFrame(w, frame); err != nil {
					return err
				}
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			// A client disconnect surfaces here as a closed pipe; treat the
			// stderr tail as the diagnostic if ffmpeg itself failed.
			if r.Context().Err() != nil {
				return nil
			}
			return fmt.Errorf("mediautil: ffmpeg stream ended: %v: %s", readErr, stderr.summary())
		}
	}
}

// writeMJPEGFrame writes a single JPEG frame as one multipart part.
func writeMJPEGFrame(w io.Writer, frame []byte) error {
	header := fmt.Sprintf("--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
		mjpegBoundary, len(frame))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if _, err := w.Write(frame); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

// splitJPEGFrames extracts every complete JPEG frame (from an SOI marker to the
// following EOI marker) contained in buf. It returns the frames found and the
// unconsumed remainder, which the caller should carry forward and prepend to
// subsequent reads so that frames split across read boundaries are reassembled.
func splitJPEGFrames(buf []byte) (frames [][]byte, rest []byte) {
	rest = buf
	for {
		soi := bytes.Index(rest, jpegSOI)
		if soi < 0 {
			// No start marker; keep a trailing 0xFF that may begin an SOI.
			if len(rest) > 0 && rest[len(rest)-1] == 0xFF {
				return frames, rest[len(rest)-1:]
			}
			return frames, nil
		}
		eoiRel := bytes.Index(rest[soi+2:], jpegEOI)
		if eoiRel < 0 {
			// Incomplete frame; retain from the SOI onward.
			return frames, rest[soi:]
		}
		end := soi + 2 + eoiRel + len(jpegEOI)
		frame := make([]byte, end-soi)
		copy(frame, rest[soi:end])
		frames = append(frames, frame)
		rest = rest[end:]
	}
}
