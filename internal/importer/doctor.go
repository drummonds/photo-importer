package importer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"codeberg.org/hum3/photo-importer/internal/config"
	"codeberg.org/hum3/photo-importer/internal/mtp"
)

type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Doctor runs preflight checks and returns one entry per check. The caller
// decides how to render and whether to exit non-zero.
func Doctor(cfg config.Config) []Check {
	var out []Check

	// Config file
	if p, err := config.Path(); err == nil {
		if _, err := os.Stat(p); err == nil {
			out = append(out, Check{Name: "config", OK: true, Detail: p})
		} else {
			out = append(out, Check{Name: "config", OK: true, Detail: "using defaults (no file at " + p + ")"})
		}
	}

	// rsync available
	if path, err := exec.LookPath("rsync"); err == nil {
		out = append(out, Check{Name: "rsync", OK: true, Detail: path})
	} else {
		out = append(out, Check{Name: "rsync", OK: false, Detail: "not found in PATH"})
	}

	// adb available + devices
	if path, err := exec.LookPath("adb"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out = append(out, Check{Name: "adb", OK: true, Detail: path})
		cmd := exec.CommandContext(ctx, "adb", "devices")
		b, err := cmd.Output()
		if err != nil {
			out = append(out, Check{Name: "adb devices", OK: false, Detail: err.Error()})
		} else {
			out = append(out, Check{Name: "adb devices", OK: true, Detail: summariseAdb(string(b))})
		}
	} else {
		out = append(out, Check{Name: "adb", OK: false, Detail: "not found in PATH (warning only)"})
	}

	// MTP mount
	mounts, err := mtp.Discover()
	switch {
	case err != nil:
		out = append(out, Check{Name: "mtp", OK: false, Detail: err.Error()})
	case len(mounts) == 0:
		out = append(out, Check{Name: "mtp", OK: false, Detail: "no MTP mount — connect phone and set USB to file transfer"})
	default:
		var hosts []string
		for _, m := range mounts {
			hosts = append(hosts, m.Host)
		}
		out = append(out, Check{Name: "mtp", OK: true, Detail: strings.Join(hosts, ", ")})
	}

	// Resolved source + destination
	if r, err := Resolve(cfg); err == nil {
		if _, err := os.Stat(r.Source); err == nil {
			out = append(out, Check{Name: "source", OK: true, Detail: r.Source})
		} else {
			out = append(out, Check{Name: "source", OK: false, Detail: fmt.Sprintf("%s: %v", r.Source, err)})
		}
		if fi, err := os.Stat(r.Destination); err == nil && fi.IsDir() {
			out = append(out, Check{Name: "destination", OK: true, Detail: r.Destination})
		} else {
			out = append(out, Check{Name: "destination", OK: true, Detail: r.Destination + " (will be created)"})
		}
	} else {
		out = append(out, Check{Name: "resolve", OK: false, Detail: err.Error()})
	}

	return out
}

func summariseAdb(s string) string {
	var devices []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		devices = append(devices, line)
	}
	if len(devices) == 0 {
		return "no devices (try replugging USB or `adb start-server`)"
	}
	return strings.Join(devices, "; ")
}
