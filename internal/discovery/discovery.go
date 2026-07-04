// Package discovery implements the ONVIF WS-Discovery (2005/04 draft) responder:
// a single UDP socket bound to :3702 and joined to the 239.255.255.250 multicast
// group answers Probe requests, and announces the proxy's virtual devices via
// Hello (on start / add) and Bye (on shutdown / removal).
package discovery

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"time"
)

// Device is the minimal identity this package advertises for one virtual
// device. XAddr is the full device service URL, e.g.
// "http://192.168.1.10:8000/onvif/device_service".
type Device struct {
	UUID     string
	Name     string
	Hardware string
	XAddr    string // full URL
}

// LogEntry records one discovery event for the web UI. Kind is one of
// probe/hello/bye/match.
type LogEntry struct {
	Time   time.Time
	Remote string
	Kind   string
	Detail string
}

// logCap is the size of the in-memory ring buffer surfaced by Log.
const logCap = 50

// Server is the WS-Discovery responder. It is safe for concurrent use.
type Server struct {
	mu        sync.Mutex
	devices   []Device
	conn      *net.UDPConn // active only while Run is executing
	multicast *net.UDPAddr
	entries   []LogEntry // newest first, capped at logCap
}

// New creates a responder for the given devices. Announcements are not sent
// until Run binds the socket.
func New(devices []Device) *Server {
	return &Server{devices: append([]Device(nil), devices...)}
}

// Run binds the multicast socket, sends a Hello for every current device, then
// serves Probe requests until ctx is cancelled, at which point it sends a Bye
// for every current device and returns ctx.Err().
func (s *Server) Run(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp4", multicastAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.conn = conn
	s.multicast = addr
	devs := append([]Device(nil), s.devices...)
	s.mu.Unlock()

	for _, d := range devs {
		if _, err := conn.WriteToUDP(buildHello(d), addr); err == nil {
			s.addLog("", "hello", d.UUID)
		}
	}

	done := make(chan struct{})
	go s.readLoop(conn, addr, done)

	<-ctx.Done()

	// Announce departure for whatever set is current at shutdown.
	s.mu.Lock()
	byeDevs := append([]Device(nil), s.devices...)
	s.conn = nil
	s.multicast = nil
	s.mu.Unlock()
	for _, d := range byeDevs {
		if _, err := conn.WriteToUDP(buildBye(d), addr); err == nil {
			s.addLog("", "bye", d.UUID)
		}
	}

	conn.Close()
	<-done
	return ctx.Err()
}

// SetDevices hot-reloads the device set. Devices are diffed by UUID: removed
// ones get a Bye, newly added ones get a Hello (only while Run is active).
func (s *Server) SetDevices(devices []Device) {
	s.mu.Lock()
	oldByUUID := make(map[string]bool, len(s.devices))
	for _, d := range s.devices {
		oldByUUID[d.UUID] = true
	}
	newByUUID := make(map[string]bool, len(devices))
	for _, d := range devices {
		newByUUID[d.UUID] = true
	}

	var removed, added []Device
	for _, d := range s.devices {
		if !newByUUID[d.UUID] {
			removed = append(removed, d)
		}
	}
	for _, d := range devices {
		if !oldByUUID[d.UUID] {
			added = append(added, d)
		}
	}

	s.devices = append([]Device(nil), devices...)
	conn, addr := s.conn, s.multicast
	s.mu.Unlock()

	// Not running yet: the table is replaced and Run will Hello on start.
	if conn == nil {
		return
	}
	for _, d := range removed {
		if _, err := conn.WriteToUDP(buildBye(d), addr); err == nil {
			s.addLog("", "bye", d.UUID)
		}
	}
	for _, d := range added {
		if _, err := conn.WriteToUDP(buildHello(d), addr); err == nil {
			s.addLog("", "hello", d.UUID)
		}
	}
}

// Log returns up to the last logCap events, newest first.
func (s *Server) Log() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// readLoop receives datagrams until the socket is closed, handling each Probe
// on its own goroutine (a ProbeMatch is delayed up to APP_MAX_DELAY).
func (s *Server) readLoop(conn *net.UDPConn, _ *net.UDPAddr, done chan struct{}) {
	defer close(done)
	buf := make([]byte, 65535)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed on shutdown
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		go s.handleProbe(conn, data, src)
	}
}

// handleProbe answers a single incoming datagram if it is a matching Probe,
// unicasting one ProbeMatches message per device back to the prober.
func (s *Server) handleProbe(conn *net.UDPConn, data []byte, src *net.UDPAddr) {
	messageID, types, ok := parseProbe(data)
	if !ok {
		return // not a Probe (e.g. our own Hello/Bye looped back)
	}
	s.addLog(src.String(), "probe", messageID)
	if !typesMatch(types) {
		return
	}

	// WS-Discovery APP_MAX_DELAY: random delay before replying.
	time.Sleep(time.Duration(rand.Intn(maxDelayMS+1)) * time.Millisecond)

	s.mu.Lock()
	devs := append([]Device(nil), s.devices...)
	s.mu.Unlock()
	for _, d := range devs {
		if _, err := conn.WriteToUDP(buildProbeMatches(d, messageID), src); err == nil {
			s.addLog(src.String(), "match", d.UUID)
		}
	}
}

// addLog prepends an event, capping the ring at logCap.
func (s *Server) addLog(remote, kind, detail string) {
	e := LogEntry{Time: time.Now(), Remote: remote, Kind: kind, Detail: detail}
	s.mu.Lock()
	s.entries = append([]LogEntry{e}, s.entries...)
	if len(s.entries) > logCap {
		s.entries = s.entries[:logCap]
	}
	s.mu.Unlock()
}
