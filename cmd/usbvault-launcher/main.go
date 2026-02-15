//go:build darwin
// +build darwin

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

const (
	defaultURL = "http://127.0.0.1:4987"
)

func main() {
	if isServerReachable(defaultURL + "/api/status") {
		_ = openBrowser(defaultURL)
		return
	}

	if err := startServerProcess(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to start USB Vault: %v\n", err)
		os.Exit(1)
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if isServerReachable(defaultURL + "/api/status") {
			_ = openBrowser(defaultURL)
			return
		}
		time.Sleep(350 * time.Millisecond)
	}

	_, _ = fmt.Fprintln(os.Stderr, "USB Vault server started but did not become ready in time")
	_ = openBrowser(defaultURL)
}

func startServerProcess() error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	execPath, _ = filepath.EvalSymlinks(execPath)
	execDir := filepath.Dir(execPath)

	serverBinary := filepath.Join(execDir, "usbvaultd")
	if _, err := os.Stat(serverBinary); err != nil {
		return fmt.Errorf("missing bundled server binary: %w", err)
	}

	resourcesDir := filepath.Join(execDir, "..", "Resources")
	webDir := filepath.Join(resourcesDir, "web")
	if _, err := os.Stat(webDir); err != nil {
		return fmt.Errorf("missing bundled web assets: %w", err)
	}

	cfgRoot, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve user config dir: %w", err)
	}
	dataDir := filepath.Join(cfgRoot, "USBVault", "data")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	logRoot, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	logDir := filepath.Join(logRoot, "Library", "Logs", "USBVault")
	if runtime.GOOS != "darwin" {
		logDir = filepath.Join(dataDir, "logs")
	}
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	logFilePath := filepath.Join(logDir, "usbvault.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(serverBinary)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"USBVAULT_DATA_DIR="+dataDir,
		"USBVAULT_WEB_DIR="+webDir,
		"USBVAULT_BIND=127.0.0.1",
	)
	cmd.SysProcAttr = detachedProcAttrs()

	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func isServerReachable(url string) bool {
	client := http.Client{Timeout: 450 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func openBrowser(url string) error {
	cmd := exec.Command("open", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func detachedProcAttrs() *syscall.SysProcAttr {
	// Launch server in a separate process group so it stays up after launcher exits.
	return &syscall.SysProcAttr{Setpgid: true}
}
