package rtspproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// startEchoBackend runs a TCP server that echoes everything back until close.
func startEchoBackend(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		close(done)
		ln.Close()
	}
}

// freePort returns an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestRun_PassThrough(t *testing.T) {
	backend, stopBackend := startEchoBackend(t)
	defer stopBackend()

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, port, backend) }()

	// Wait for the proxy listener to come up.
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	conn := dialWithRetry(t, proxyAddr)
	defer conn.Close()

	msg := []byte("hello rtsp proxy")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("echo = %q, want %q", buf, msg)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after cancel")
	}
}

func TestRun_CancelClosesConnections(t *testing.T) {
	backend, stopBackend := startEchoBackend(t)
	defer stopBackend()

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, port, backend) }()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	conn := dialWithRetry(t, proxyAddr)
	defer conn.Close()

	// Verify the connection is live.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	cancel()

	// After cancel the in-flight connection must be closed by the proxy:
	// a subsequent read should hit EOF (or a reset), not block.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := conn.Read(make([]byte, 16))
	if err == nil {
		t.Error("expected connection to be closed after cancel")
	}

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after cancel")
	}
}

func dialWithRetry(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial proxy %s: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
