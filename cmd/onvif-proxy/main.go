// Command onvif-proxy exposes RTSP streams as virtual ONVIF Profile S
// devices. See docs/ for the full design.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/config"
	"github.com/Aiaid/onvif-proxy/internal/discovery"
	"github.com/Aiaid/onvif-proxy/internal/mediautil"
	"github.com/Aiaid/onvif-proxy/internal/onvif"
	"github.com/Aiaid/onvif-proxy/internal/rtspproxy"
	"github.com/Aiaid/onvif-proxy/internal/web"
)

var version = "dev"

func main() {
	configPath := flag.String("config", envOr("CONFIG", "./config.yaml"), "path to config.yaml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		if err := config.WriteDefault(*configPath); err != nil {
			log.Fatalf("generate default config: %v", err)
		}
		log.Printf("no config found — generated %s, add devices via the web UI", *configPath)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	overrides, err := config.ApplyEnvOverrides(cfg)
	for _, o := range overrides {
		log.Printf("config: %s", o)
	}
	if err != nil {
		log.Fatalf("env overrides: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	m := newManager(*configPath, cfg)
	if err := m.start(ctx); err != nil {
		log.Fatalf("start: %v", err)
	}

	if cfg.Web.IsEnabled() {
		ws := web.New(cfg.Web, m)
		go func() {
			if err := ws.Run(ctx); err != nil && ctx.Err() == nil {
				log.Fatalf("web server: %v", err)
			}
		}()
		log.Printf("web UI listening on :%d", cfg.Web.Port)
	}

	<-ctx.Done()
	log.Print("shutting down")
	m.stopAll()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// manager owns the running set of virtual devices and implements web.Backend.
type manager struct {
	configPath  string
	startedAt   time.Time
	advertiseIP string

	mu      sync.Mutex
	cfg     *config.Config
	cancel  context.CancelFunc // cancels the current device generation
	done    chan struct{}      // closed when the current generation fully stopped
	disc    *discovery.Server
	caches  map[string]*mediautil.Cache // device uuid -> snapshot cache
	rootCtx context.Context
}

func newManager(path string, cfg *config.Config) *manager {
	adv := cfg.Server.AdvertiseIP
	if adv == "" {
		adv = config.DetectAdvertiseIP()
	}
	return &manager{
		configPath:  path,
		startedAt:   time.Now(),
		advertiseIP: adv,
		cfg:         cfg,
		caches:      map[string]*mediautil.Cache{},
	}
}

// start launches discovery plus the initial device generation.
func (m *manager) start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rootCtx = ctx

	if m.cfg.Server.DiscoveryEnabled() {
		m.disc = discovery.New(m.discoveryDevices(m.cfg))
		go func() {
			if err := m.disc.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("ws-discovery: %v", err)
			}
		}()
	}
	m.launchLocked(m.cfg)
	return nil
}

// launchLocked starts one generation of device servers/proxies. mu held.
func (m *manager) launchLocked(cfg *config.Config) {
	genCtx, cancel := context.WithCancel(m.rootCtx)
	m.cancel = cancel
	done := make(chan struct{})
	m.done = done
	m.cfg = cfg

	var wg sync.WaitGroup
	for _, dev := range cfg.Devices {
		dev := dev
		if _, ok := m.caches[dev.UUID]; !ok {
			m.caches[dev.UUID] = mediautil.NewCache(time.Duration(dev.SnapshotCacheSeconds()) * time.Second)
		}
		srv := onvif.NewServer(dev, onvif.Options{
			AdvertiseIP: m.advertiseIP,
			Version:     version,
			SnapshotFunc: func(ctx context.Context, streamName string) ([]byte, string, error) {
				return m.Snapshot(ctx, dev, streamName)
			},
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Run(genCtx); err != nil && genCtx.Err() == nil {
				log.Printf("device %q soap server: %v", dev.Name, err)
			}
		}()
		for _, b := range dev.ProxyBindings() {
			b := b
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := rtspproxy.Run(genCtx, b.ListenPort, b.Target); err != nil && genCtx.Err() == nil {
					log.Printf("device %q rtsp proxy :%d -> %s: %v", dev.Name, b.ListenPort, b.Target, err)
				}
			}()
		}
		log.Printf("device %q: soap :%d, rtsp proxy %v, %d profile(s)",
			dev.Name, dev.Ports.SOAP, dev.ProxyBindings(), len(dev.Streams))
	}
	go func() { wg.Wait(); close(done) }()
}

func (m *manager) stopAll() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (m *manager) discoveryDevices(cfg *config.Config) []discovery.Device {
	out := make([]discovery.Device, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		out = append(out, discovery.Device{
			UUID:     d.UUID,
			Name:     d.Name,
			Hardware: d.Info.Model,
			XAddr:    fmt.Sprintf("http://%s:%d/onvif/device_service", m.advertiseIP, d.Ports.SOAP),
		})
	}
	return out
}

// ---- web.Backend implementation -------------------------------------------

func (m *manager) ConfigYAML() ([]byte, error) {
	return os.ReadFile(m.configPath)
}

func (m *manager) ApplyConfig(raw []byte, dryRun bool) error {
	cfg, err := config.Parse(raw)
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}

	// Persist the user's text as-is, then reload through the normal path so
	// generated identities get filled and written back.
	if err := os.WriteFile(m.configPath, raw, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	cfg, err = config.Load(m.configPath)
	if err != nil {
		return err
	}
	// Same env layer as startup, so a reload cannot silently drop overrides
	// (e.g. a device edit claiming the env-overridden web port surfaces here).
	if _, err := config.ApplyEnvOverrides(cfg); err != nil {
		return err
	}

	// stop-then-start reload of the device generation.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
			log.Print("reload: previous generation did not stop in time")
		}
	}
	m.launchLocked(cfg)
	if m.disc != nil {
		m.disc.SetDevices(m.discoveryDevices(cfg))
	}
	log.Printf("config reloaded: %d device(s)", len(cfg.Devices))
	return nil
}

func (m *manager) Devices() []web.DeviceRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]web.DeviceRuntime, 0, len(m.cfg.Devices))
	for _, d := range m.cfg.Devices {
		out = append(out, web.DeviceRuntime{Device: d, Running: true})
	}
	return out
}

// Snapshot serves the ONVIF GetSnapshotUri endpoint and the web UI: camera
// passthrough when snapshot.url is configured, otherwise a cached ffmpeg grab.
func (m *manager) Snapshot(ctx context.Context, dev *config.Device, streamName string) ([]byte, string, error) {
	if dev.Snapshot.URL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, dev.Snapshot.URL, nil)
		if err != nil {
			return nil, "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("snapshot passthrough: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("snapshot passthrough: upstream %s", resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			return nil, "", err
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "image/jpeg"
		}
		return data, ct, nil
	}

	stream := dev.SnapshotStream()
	if streamName != "" {
		if s := dev.StreamByName(streamName); s != nil {
			stream = s
		}
	}
	m.mu.Lock()
	cache := m.caches[dev.UUID]
	m.mu.Unlock()
	data, err := cache.Get(stream.Name, func() ([]byte, error) {
		return mediautil.Grab(ctx, stream.SourceURL())
	})
	if err != nil {
		return nil, "", err
	}
	return data, "image/jpeg", nil
}

func (m *manager) DiscoveryLog() []discovery.LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.disc == nil {
		return nil
	}
	return m.disc.Log()
}

func (m *manager) Status() web.Status {
	return web.Status{
		Version:       version,
		AdvertiseIP:   m.advertiseIP,
		UptimeSeconds: int64(time.Since(m.startedAt).Seconds()),
		FFmpeg:        mediautil.Available(),
	}
}
