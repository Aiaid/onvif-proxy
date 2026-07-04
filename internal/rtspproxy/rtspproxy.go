// Package rtspproxy provides a minimal TCP pass-through proxy. It listens on a
// port and forwards every accepted connection bidirectionally to a fixed
// upstream target ("host:port"), which is how ONVIF clients reach the real
// camera RTSP stream through the advertised proxy port.
package rtspproxy

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const dialTimeout = 5 * time.Second

// Run listens on all interfaces at listenPort and forwards each connection to
// target via two io.Copy goroutines; when either direction closes, both sides
// are closed. Cancelling ctx closes the listener and every in-flight
// connection. It returns the net.Listen error or, on shutdown, ctx.Err().
func Run(ctx context.Context, listenPort int, target string) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", ":"+strconv.Itoa(listenPort))
	if err != nil {
		return err
	}

	var (
		mu     sync.Mutex
		conns  = make(map[net.Conn]struct{})
		wg     sync.WaitGroup
		closed bool
	)

	track := func(c net.Conn) {
		mu.Lock()
		conns[c] = struct{}{}
		mu.Unlock()
	}
	untrack := func(c net.Conn) {
		mu.Lock()
		delete(conns, c)
		mu.Unlock()
	}

	// Close the listener and all tracked connections when ctx is cancelled.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
			return
		}
		mu.Lock()
		closed = true
		ln.Close()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()

	for {
		client, err := ln.Accept()
		if err != nil {
			mu.Lock()
			isClosed := closed
			mu.Unlock()
			if isClosed {
				break
			}
			// Back off briefly on temporary accept errors, then retry.
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			break
		}

		wg.Add(1)
		go func(client net.Conn) {
			defer wg.Done()
			track(client)
			defer func() {
				untrack(client)
				client.Close()
			}()

			upstream, err := net.DialTimeout("tcp", target, dialTimeout)
			if err != nil {
				return
			}
			track(upstream)
			defer func() {
				untrack(upstream)
				upstream.Close()
			}()

			// Bidirectional copy; closing on either side unblocks the other.
			var once sync.Once
			closeBoth := func() {
				once.Do(func() {
					client.Close()
					upstream.Close()
				})
			}

			var pipeWg sync.WaitGroup
			pipeWg.Add(2)
			go func() {
				defer pipeWg.Done()
				io.Copy(upstream, client)
				closeBoth()
			}()
			go func() {
				defer pipeWg.Done()
				io.Copy(client, upstream)
				closeBoth()
			}()
			pipeWg.Wait()
		}(client)
	}

	close(stop)
	wg.Wait()
	return ctx.Err()
}
