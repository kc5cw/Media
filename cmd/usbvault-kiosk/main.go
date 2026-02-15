package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"businessplan/usbvault/internal/display"
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Println("usbvault-kiosk is supported on Linux (Raspberry Pi) only")
		return
	}

	baseURL := strings.TrimSpace(os.Getenv("USBVAULT_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:4987"
	}

	st := display.Detect()
	if !hasAnyDisplay(st) {
		// Headless mode: do nothing (server can still run).
		return
	}

	url := baseURL + "/"
	if preferTouchUI(st) {
		url = baseURL + "/web/touch/touch.html"
	}

	// Best effort: wait for server to come up.
	waitFor(baseURL+"/api/status", 25*time.Second)

	browser, args, err := kioskCommand(url)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "no supported browser found for kiosk mode: %v\n", err)
		return
	}

	cmd := exec.Command(browser, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to start kiosk browser: %v\n", err)
		return
	}
	_ = cmd.Process.Release()
}

func hasAnyDisplay(st display.State) bool {
	if st.HasHDMI || st.HasDSI {
		return true
	}
	// Some GPIO/SPI tiny screens appear only as fb1+.
	return len(st.Framebuffers) > 0
}

func preferTouchUI(st display.State) bool {
	// Prefer the Mac-like UI on HDMI.
	if st.HasHDMI {
		return false
	}
	// If there is touch and some non-HDMI display (DSI or framebuffer), use touch UI.
	if st.HasTouch && (st.HasDSI || len(st.Framebuffers) > 0) {
		return true
	}
	return false
}

func waitFor(url string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 600 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(350 * time.Millisecond)
	}
}

func kioskCommand(url string) (string, []string, error) {
	// Raspberry Pi OS often uses "chromium-browser" or "chromium".
	candidates := [][]string{
		{"chromium-browser"},
		{"chromium"},
		{"google-chrome"},
		{"firefox"},
	}

	var exe string
	for _, c := range candidates {
		p, err := exec.LookPath(c[0])
		if err == nil {
			exe = p
			break
		}
	}
	if exe == "" {
		return "", nil, fmt.Errorf("chromium/chrome/firefox not found in PATH")
	}

	base := filepath.Base(exe)
	switch {
	case strings.Contains(base, "chromium") || strings.Contains(base, "chrome"):
		return exe, []string{
			"--kiosk",
			"--no-first-run",
			"--disable-infobars",
			"--disable-session-crashed-bubble",
			"--autoplay-policy=no-user-gesture-required",
			"--check-for-update-interval=31536000",
			"--app=" + url,
		}, nil
	case strings.Contains(base, "firefox"):
		// Firefox kiosk support varies; start fullscreen.
		return exe, []string{"--kiosk", url}, nil
	default:
		return "", nil, fmt.Errorf("unsupported browser: %s", exe)
	}
}
