package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"businessplan/usbvault/internal/audit"
	"businessplan/usbvault/internal/config"
	"businessplan/usbvault/internal/db"
	"businessplan/usbvault/internal/media"
)

const baseStorageSetting = "base_storage_dir"

type Manager struct {
	store      *db.Store
	audit      *audit.Logger
	logger     *log.Logger
	jobs       chan string
	processing sync.Map
}

type Result struct {
	Scanned    int `json:"scanned"`
	Copied     int `json:"copied"`
	Duplicates int `json:"duplicates"`
	Errors     int `json:"errors"`
}

func NewManager(store *db.Store, auditLogger *audit.Logger, logger *log.Logger) *Manager {
	return &Manager{
		store:  store,
		audit:  auditLogger,
		logger: logger,
		jobs:   make(chan string, 16),
	}
}

func (m *Manager) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case mount := <-m.jobs:
				if _, loaded := m.processing.LoadOrStore(config.PathKey(mount), struct{}{}); loaded {
					continue
				}
				go func(mountPath string) {
					defer m.processing.Delete(config.PathKey(mountPath))
					res, err := m.ProcessMount(ctx, mountPath, "system")
					if err != nil {
						m.logger.Printf("ingest mount %s failed: %v", mountPath, err)
						return
					}
					m.logger.Printf("ingested mount %s scanned=%d copied=%d duplicates=%d errors=%d", mountPath, res.Scanned, res.Copied, res.Duplicates, res.Errors)
				}(mount)
			}
		}
	}()
}

func (m *Manager) QueueMount(mountPath string) {
	select {
	case m.jobs <- mountPath:
	default:
		m.logger.Printf("ingest queue full, dropping mount event: %s", mountPath)
	}
}

func (m *Manager) ProcessMount(ctx context.Context, mountPath, actor string) (Result, error) {
	mountPath = filepath.Clean(mountPath)
	var result Result

	baseStorage, ok, err := m.store.GetSetting(ctx, baseStorageSetting)
	if err != nil {
		return result, err
	}
	if !ok || strings.TrimSpace(baseStorage) == "" {
		_ = m.audit.Log(ctx, actor, "ingest_skipped_no_storage", map[string]any{"mount": mountPath})
		return result, nil
	}

	baseStorage = filepath.Clean(baseStorage)
	if err := os.MkdirAll(baseStorage, 0o750); err != nil {
		return result, fmt.Errorf("ensure base storage: %w", err)
	}

	if config.PathKey(baseStorage) == config.PathKey(mountPath) {
		return result, nil
	}

	_ = m.audit.Log(ctx, actor, "ingest_started", map[string]any{"mount": mountPath})

	walkErr := filepath.WalkDir(mountPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Errors++
			return nil
		}

		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		kind, supported := config.IsSupportedMedia(path)
		if !supported {
			return nil
		}
		result.Scanned++

		if err := m.ingestFile(ctx, mountPath, baseStorage, path, kind, actor, &result); err != nil {
			result.Errors++
			m.logger.Printf("ingest file error %s: %v", path, err)
		}
		return nil
	})

	if walkErr != nil {
		return result, walkErr
	}

	_ = m.audit.Log(ctx, actor, "ingest_completed", map[string]any{
		"mount":      mountPath,
		"scanned":    result.Scanned,
		"copied":     result.Copied,
		"duplicates": result.Duplicates,
		"errors":     result.Errors,
	})
	return result, nil
}

func (m *Manager) ingestFile(ctx context.Context, mountPath, baseStorage, srcPath, kind, actor string, result *Result) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	crcHex, shaHex, err := media.ComputeHashes(srcPath)
	if err != nil {
		return err
	}

	meta, err := media.ExtractMetadata(srcPath, kind)
	if err != nil {
		return err
	}
	capture := normalizeCaptureTime(meta.CaptureTime, info.ModTime())

	exists, err := m.store.MediaExists(ctx, crcHex, info.Size(), capture)
	if err != nil {
		return err
	}
	if exists {
		result.Duplicates++
		_ = m.audit.Log(ctx, actor, "duplicate_skipped", map[string]any{
			"source_path":  srcPath,
			"crc32":        crcHex,
			"capture_time": capture,
		})
		return nil
	}

	destPath, err := buildDestinationPath(baseStorage, capture, srcPath, shaHex)
	if err != nil {
		return err
	}
	if err := copyFileAtomic(srcPath, destPath, info.Mode(), info.ModTime()); err != nil {
		return err
	}

	rec := &db.MediaRecord{
		Kind:        kind,
		FileName:    filepath.Base(srcPath),
		Extension:   strings.ToLower(filepath.Ext(srcPath)),
		SourceMount: mountPath,
		SourcePath:  srcPath,
		DestPath:    destPath,
		SizeBytes:   info.Size(),
		CRC32:       crcHex,
		SHA256:      shaHex,
		CaptureTime: capture,
		GPSLat:      meta.GPSLat,
		GPSLon:      meta.GPSLon,
		Make:        meta.Make,
		Model:       meta.Model,
		CameraYaw:   meta.CameraYaw,
		CameraPitch: meta.CameraPitch,
		CameraRoll:  meta.CameraRoll,
		Metadata:    meta.RawJSON,
		SourceMTime: info.ModTime().UTC().Format(time.RFC3339),
		IngestedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if err := m.store.InsertMedia(ctx, rec); err != nil {
		_ = os.Remove(destPath)
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			result.Duplicates++
			return nil
		}
		return err
	}

	result.Copied++
	_ = m.audit.Log(ctx, actor, "file_ingested", map[string]any{
		"source_path":  srcPath,
		"dest_path":    destPath,
		"crc32":        crcHex,
		"capture_time": capture,
	})
	return nil
}

func normalizeCaptureTime(raw string, fallback time.Time) string {
	if raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return fallback.UTC().Format(time.RFC3339)
}

func buildDestinationPath(baseStorage, capture, sourcePath, shaHex string) (string, error) {
	tm, err := time.Parse(time.RFC3339, capture)
	if err != nil {
		tm = time.Now().UTC()
	}

	folder := filepath.Join(baseStorage, tm.Format("2006"), tm.Format("01"), tm.Format("02"))
	if err := os.MkdirAll(folder, 0o750); err != nil {
		return "", err
	}

	name := sanitizeFilename(filepath.Base(sourcePath))
	ext := strings.ToLower(filepath.Ext(name))
	base := strings.TrimSuffix(name, ext)
	if base == "" {
		base = "media"
	}

	shortHash := "unknown"
	if len(shaHex) >= 8 {
		shortHash = shaHex[:8]
	}

	candidate := filepath.Join(folder, fmt.Sprintf("%s_%s%s", base, shortHash, ext))
	if !fileExists(candidate) {
		return candidate, nil
	}

	for i := 1; i <= 10000; i++ {
		alt := filepath.Join(folder, fmt.Sprintf("%s_%s_%d%s", base, shortHash, i, ext))
		if !fileExists(alt) {
			return alt, nil
		}
	}

	return "", errors.New("unable to allocate destination filename")
}

func copyFileAtomic(srcPath, dstPath string, srcMode fs.FileMode, modTime time.Time) error {
	tmpPath := dstPath + ".part"

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}

	copyErr := func() error {
		defer dst.Close()
		if _, err := io.Copy(dst, src); err != nil {
			return err
		}
		if err := dst.Sync(); err != nil {
			return err
		}
		return nil
	}()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}

	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	_ = os.Chtimes(dstPath, modTime, modTime)
	_ = os.Chmod(dstPath, 0o440)
	_ = srcMode
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	return strings.Trim(name, "_.")
}
