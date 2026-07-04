package config

import (
	"strings"
	"testing"
)

const envTestYAML = `
web:
  port: 8080
devices:
  - name: cam
    ports: { soap: 8081, rtsp: 8554 }
    streams:
      - name: main
        rtsp: rtsp://192.168.1.50:554/main
        width: 1920
        height: 1080
        framerate: 25
        bitrate: 2048
`

// clearEnv pins every override variable to empty so values leaking in from the
// developer's shell cannot affect the test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ONVIF_ADVERTISE_IP", "ONVIF_DISCOVERY", "ONVIF_WEB_ENABLED",
		"ONVIF_WEB_PORT", "ONVIF_WEB_USERNAME", "ONVIF_WEB_PASSWORD",
	} {
		t.Setenv(k, "")
	}
}

func parseEnvTestConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(envTestYAML))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return cfg
}

func TestApplyEnvOverridesAll(t *testing.T) {
	clearEnv(t)
	t.Setenv("ONVIF_ADVERTISE_IP", "10.0.0.9")
	t.Setenv("ONVIF_DISCOVERY", "false")
	t.Setenv("ONVIF_WEB_ENABLED", "false")
	t.Setenv("ONVIF_WEB_PORT", "9090")
	t.Setenv("ONVIF_WEB_USERNAME", "admin")
	t.Setenv("ONVIF_WEB_PASSWORD", "secret")

	cfg := parseEnvTestConfig(t)
	applied, err := ApplyEnvOverrides(cfg)
	if err != nil {
		t.Fatalf("ApplyEnvOverrides: %v", err)
	}
	if len(applied) != 6 {
		t.Errorf("applied = %v, want 6 entries", applied)
	}
	if cfg.Server.AdvertiseIP != "10.0.0.9" {
		t.Errorf("advertise_ip = %q", cfg.Server.AdvertiseIP)
	}
	if cfg.Server.DiscoveryEnabled() {
		t.Error("discovery should be disabled")
	}
	if cfg.Web.IsEnabled() {
		t.Error("web should be disabled")
	}
	if cfg.Web.Port != 9090 {
		t.Errorf("web.port = %d, want 9090", cfg.Web.Port)
	}
	if cfg.Web.Username != "admin" || cfg.Web.Password != "secret" {
		t.Errorf("web auth = %q/%q", cfg.Web.Username, cfg.Web.Password)
	}
	for _, a := range applied {
		if strings.Contains(a, "secret") {
			t.Errorf("applied log leaks a value: %q", a)
		}
	}
}

func TestApplyEnvOverridesUnsetLeavesConfig(t *testing.T) {
	clearEnv(t)
	cfg := parseEnvTestConfig(t)
	applied, err := ApplyEnvOverrides(cfg)
	if err != nil {
		t.Fatalf("ApplyEnvOverrides: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied = %v, want none", applied)
	}
	if cfg.Web.Port != 8080 || !cfg.Web.IsEnabled() || !cfg.Server.DiscoveryEnabled() {
		t.Error("config changed without any override set")
	}
}

func TestApplyEnvOverridesBadValues(t *testing.T) {
	cases := []struct{ key, val string }{
		{"ONVIF_DISCOVERY", "yep"},
		{"ONVIF_WEB_ENABLED", "2"},
		{"ONVIF_WEB_PORT", "notaport"},
		{"ONVIF_WEB_PORT", "70000"},
		{"ONVIF_WEB_PORT", "0"},
	}
	for _, c := range cases {
		t.Run(c.key+"="+c.val, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(c.key, c.val)
			if _, err := ApplyEnvOverrides(parseEnvTestConfig(t)); err == nil {
				t.Errorf("%s=%q: want error, got nil", c.key, c.val)
			}
		})
	}
}

func TestApplyEnvOverridesRevalidates(t *testing.T) {
	t.Run("port conflict", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("ONVIF_WEB_PORT", "8081") // collides with devices[0].ports.soap
		if _, err := ApplyEnvOverrides(parseEnvTestConfig(t)); err == nil {
			t.Error("want port-conflict error, got nil")
		}
	})
	t.Run("password without username", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("ONVIF_WEB_PASSWORD", "secret")
		if _, err := ApplyEnvOverrides(parseEnvTestConfig(t)); err == nil {
			t.Error("want pairing error, got nil")
		}
	})
}
