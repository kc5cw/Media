package usb

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"businessplan/usbvault/internal/config"
)

type Watcher struct {
	logger   *log.Logger
	interval time.Duration
	roots    []string
	seen     map[string]time.Time
	onNew    func(string)
}

func NewWatcher(interval time.Duration, logger *log.Logger, onNew func(string)) *Watcher {
	return &Watcher{
		logger:   logger,
		interval: interval,
		roots:    config.MountRoots(),
		seen:     map[string]time.Time{},
		onNew:    onNew,
	}
}

func (w *Watcher) Start(ctx context.Context) {
	w.tick(ctx)
	ticker := time.NewTicker(w.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.tick(ctx)
			}
		}
	}()
}

func (w *Watcher) tick(ctx context.Context) {
	_ = ctx
	current := map[string]struct{}{}
	mounts := w.discoverMounts()

	for _, mount := range mounts {
		key := config.PathKey(mount)
		current[key] = struct{}{}
		if _, known := w.seen[key]; !known {
			w.seen[key] = time.Now()
			w.logger.Printf("new removable volume detected: %s", mount)
			if w.onNew != nil {
				w.onNew(mount)
			}
		}
	}

	for key := range w.seen {
		if _, ok := current[key]; !ok {
			delete(w.seen, key)
		}
	}
}

func (w *Watcher) discoverMounts() []string {
	if runtime.GOOS == "windows" {
		return discoverWindowsDrives(w.roots)
	}

	mounts := make([]string, 0)
	for _, root := range w.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if shouldSkipMountName(name) {
				continue
			}
			mounts = append(mounts, filepath.Join(root, name))
		}
	}
	return mounts
}

func discoverWindowsDrives(letters []string) []string {
	mounts := make([]string, 0)
	for _, drive := range letters {
		if strings.EqualFold(drive, "C:\\") {
			continue
		}
		if stat, err := os.Stat(drive); err == nil && stat.IsDir() {
			mounts = append(mounts, drive)
		}
	}
	return mounts
}

func shouldSkipMountName(name string) bool {
	if name == "" {
		return true
	}
	lower := strings.ToLower(name)
	if lower == "macintosh hd" || strings.HasPrefix(lower, ".") {
		return true
	}
	if strings.Contains(lower, "snapshot") {
		return true
	}
	return false
}
