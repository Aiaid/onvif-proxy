package config

import (
	"fmt"
	"os"
	"strconv"
)

// ApplyEnvOverrides mutates cfg in memory from ONVIF_* environment variables
// (docs/03-config.md §3) and re-validates the result. It returns a description
// of each applied override for logging (values are omitted — passwords).
//
// Overrides must never reach the config file. Every persistence path re-parses
// the file text (web device edits) or marshals a config that never had
// overrides applied (Load's identity write-back), so calling this only on the
// runtime copy keeps env values out of config.yaml.
func ApplyEnvOverrides(cfg *Config) ([]string, error) {
	var applied, errs []string

	setStr := func(key string, dst *string, field string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
			applied = append(applied, key+" overrides "+field)
		}
	}
	setBool := func(key string, dst **bool, field string) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %q is not a boolean", key, v))
			return
		}
		*dst = &b
		applied = append(applied, key+" overrides "+field)
	}
	setPort := func(key string, dst *int, field string) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 65535 {
			errs = append(errs, fmt.Sprintf("%s: %q is not a port (1-65535)", key, v))
			return
		}
		*dst = n
		applied = append(applied, key+" overrides "+field)
	}

	setStr("ONVIF_ADVERTISE_IP", &cfg.Server.AdvertiseIP, "server.advertise_ip")
	setBool("ONVIF_DISCOVERY", &cfg.Server.Discovery, "server.discovery")
	setBool("ONVIF_WEB_ENABLED", &cfg.Web.Enabled, "web.enabled")
	setPort("ONVIF_WEB_PORT", &cfg.Web.Port, "web.port")
	setStr("ONVIF_WEB_USERNAME", &cfg.Web.Username, "web.username")
	setStr("ONVIF_WEB_PASSWORD", &cfg.Web.Password, "web.password")

	if len(errs) > 0 {
		return applied, &ValidationError{Problems: errs}
	}
	if len(applied) > 0 {
		if err := cfg.Validate(); err != nil {
			return applied, fmt.Errorf("config invalid after env overrides: %w", err)
		}
	}
	return applied, nil
}
