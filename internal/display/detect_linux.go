//go:build linux
// +build linux

package display

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type State struct {
	HasHDMI      bool
	HasDSI       bool
	HasAnyDRM    bool
	Framebuffers []string
	HasTouch     bool
}

func Detect() State {
	st := State{}

	// DRM connector status (KMS): /sys/class/drm/card0-HDMI-A-1/status, ...
	// Values are typically "connected" or "disconnected".
	drmEntries, _ := filepath.Glob("/sys/class/drm/*/status")
	for _, statusPath := range drmEntries {
		b, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}
		st.HasAnyDRM = true
		if strings.TrimSpace(string(b)) != "connected" {
			continue
		}
		base := strings.ToLower(filepath.Base(filepath.Dir(statusPath)))
		// Connector directory name contains connector type.
		if strings.Contains(base, "hdmi") {
			st.HasHDMI = true
		}
		if strings.Contains(base, "dsi") {
			st.HasDSI = true
		}
	}

	// Framebuffer devices (covers some SPI/GPIO-driven tiny TFT screens that expose fb1, etc.).
	fbEntries, _ := filepath.Glob("/sys/class/graphics/fb*")
	for _, fb := range fbEntries {
		st.Framebuffers = append(st.Framebuffers, fb)
	}

	// Touchscreen detection (best-effort) via /proc/bus/input/devices.
	st.HasTouch = detectTouchViaProc()

	return st
}

func detectTouchViaProc() bool {
	f, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var (
		nameLine    string
		handlers    string
		hasABS      bool
		blockHasEvt bool
	)

	flush := func() bool {
		nameLower := strings.ToLower(nameLine)
		if strings.Contains(nameLower, "touch") || strings.Contains(nameLower, "touchscreen") {
			// Most touch devices also expose ABS axes and event handlers.
			if strings.Contains(handlers, "event") {
				return true
			}
		}
		if hasABS && blockHasEvt {
			// Heuristic fallback: absolute pointer with event handler.
			if strings.Contains(nameLower, "ilitek") || strings.Contains(nameLower, "goodix") || strings.Contains(nameLower, "fts") {
				return true
			}
		}
		return false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if flush() {
				return true
			}
			nameLine = ""
			handlers = ""
			hasABS = false
			blockHasEvt = false
			continue
		}

		if strings.HasPrefix(line, "N: Name=") {
			nameLine = line
		}
		if strings.HasPrefix(line, "H: Handlers=") {
			handlers = line
			if strings.Contains(line, "event") {
				blockHasEvt = true
			}
		}
		if strings.HasPrefix(line, "B: ABS=") {
			hasABS = true
		}
	}

	return flush()
}
