package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	DefaultPort            = 4987
	DefaultUSBScanInterval = 10
	DefaultSessionTTLHours = 12
)

var SupportedImageExtensions = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".jpe": {}, ".png": {}, ".tif": {}, ".tiff": {}, ".bmp": {}, ".webp": {}, ".gif": {},
	".heic": {}, ".heif": {}, ".dng": {}, ".arw": {}, ".cr2": {}, ".cr3": {}, ".nef": {}, ".orf": {}, ".raf": {}, ".rw2": {},
	".srw": {}, ".x3f": {}, ".3fr": {}, ".iiq": {}, ".pef": {}, ".hdr": {}, ".exr": {}, ".jp2": {},
}

var SupportedVideoExtensions = map[string]struct{}{
	".mp4": {}, ".mov": {}, ".m4v": {}, ".ts": {}, ".m2ts": {}, ".mts": {}, ".mpeg": {}, ".mpg": {}, ".avi": {}, ".mkv": {}, ".mxf": {},
	".wmv": {}, ".webm": {}, ".lrv": {}, ".insv": {}, ".flv": {}, ".3gp": {},
}

func IsSupportedMedia(path string) (kind string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if _, exists := SupportedImageExtensions[ext]; exists {
		return "image", true
	}
	if _, exists := SupportedVideoExtensions[ext]; exists {
		return "video", true
	}
	return "", false
}

func Port() int {
	if raw := os.Getenv("USBVAULT_PORT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 65535 {
			return parsed
		}
	}
	return DefaultPort
}

func USBScanIntervalSeconds() int {
	if raw := os.Getenv("USBVAULT_SCAN_INTERVAL_SECONDS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 2 {
			return parsed
		}
	}
	return DefaultUSBScanInterval
}

func DataDir() string {
	if raw := os.Getenv("USBVAULT_DATA_DIR"); raw != "" {
		return raw
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "data"
	}
	return filepath.Join(cwd, "data")
}

func DBPath() string {
	return filepath.Join(DataDir(), "usbvault.db")
}

func MountRoots() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Volumes"}
	case "windows":
		letters := make([]string, 0, 26)
		for c := 'A'; c <= 'Z'; c++ {
			letters = append(letters, string(c)+":\\")
		}
		return letters
	default:
		return []string{"/media", "/run/media", "/mnt", "/Volumes"}
	}
}

func PathKey(path string) string {
	normalized := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(normalized)
	}
	return normalized
}
