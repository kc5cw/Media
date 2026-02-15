package ingest

import (
	"context"
	"database/sql"
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
	"businessplan/usbvault/internal/geocode"
	"businessplan/usbvault/internal/media"
)

const baseStorageSetting = "base_storage_dir"
const storageLayoutSetting = "storage_layout"

const (
	storageLayoutDate         = "date"
	storageLayoutLocationDate = "location_date"
)

type Manager struct {
	store      *db.Store
	audit      *audit.Logger
	geocoder   *geocode.ReverseGeocoder
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

func NewManager(store *db.Store, auditLogger *audit.Logger, geocoder *geocode.ReverseGeocoder, logger *log.Logger) *Manager {
	return &Manager{
		store:    store,
		audit:    auditLogger,
		geocoder: geocoder,
		logger:   logger,
		jobs:     make(chan string, 16),
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

	layout := storageLayoutLocationDate
	if raw, ok, err := m.store.GetSetting(ctx, storageLayoutSetting); err == nil && ok {
		layout = normalizeStorageLayout(raw)
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

		if err := m.ingestFile(ctx, mountPath, baseStorage, layout, path, kind, actor, &result); err != nil {
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

func (m *Manager) ingestFile(ctx context.Context, mountPath, baseStorage, layout, srcPath, kind, actor string, result *Result) error {
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

	rec := &db.MediaRecord{
		Kind:        kind,
		FileName:    filepath.Base(srcPath),
		Extension:   strings.ToLower(filepath.Ext(srcPath)),
		SourceMount: mountPath,
		SourcePath:  srcPath,
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

	if meta.GPSLat.Valid && meta.GPSLon.Valid {
		if loc, err := m.geocoder.Reverse(ctx, meta.GPSLat.Float64, meta.GPSLon.Float64); err == nil && loc != nil {
			rec.LocProvider = toNullString(loc.Provider)
			rec.Country = toNullString(loc.Country)
			rec.State = toNullString(loc.State)
			rec.County = toNullString(loc.County)
			rec.City = toNullString(loc.City)
			rec.Road = toNullString(loc.Road)
			rec.HouseNumber = toNullString(loc.HouseNumber)
			rec.Postcode = toNullString(loc.Postcode)
			rec.DisplayName = toNullString(loc.DisplayName)
		}
	}

	destPath, err := buildDestinationPath(baseStorage, layout, capture, srcPath, shaHex, rec)
	if err != nil {
		return err
	}
	if err := copyFileAtomic(srcPath, destPath, info.Mode(), info.ModTime()); err != nil {
		return err
	}
	rec.DestPath = destPath

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

func toNullString(v string) sql.NullString {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func normalizeCaptureTime(raw string, fallback time.Time) string {
	if raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return fallback.UTC().Format(time.RFC3339)
}

func buildDestinationPath(baseStorage, layout, capture, sourcePath, shaHex string, rec *db.MediaRecord) (string, error) {
	tm, err := time.Parse(time.RFC3339, capture)
	if err != nil {
		tm = time.Now().UTC()
	}

	folder := filepath.Join(baseStorage, tm.Format("2006"), tm.Format("01"), tm.Format("02"))
	if normalizeStorageLayout(layout) == storageLayoutLocationDate {
		locParts := buildLocationFolderParts(rec)
		if len(locParts) > 0 {
			folder = filepath.Join(append([]string{baseStorage}, append(locParts, tm.Format("2006"), tm.Format("01"), tm.Format("02"))...)...)
		} else {
			folder = filepath.Join(baseStorage, "Unknown", tm.Format("2006"), tm.Format("01"), tm.Format("02"))
		}
	}
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

func normalizeStorageLayout(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	switch raw {
	case storageLayoutDate:
		return storageLayoutDate
	case storageLayoutLocationDate:
		return storageLayoutLocationDate
	case "":
		return storageLayoutLocationDate
	default:
		return storageLayoutLocationDate
	}
}

func buildLocationFolderParts(rec *db.MediaRecord) []string {
	parts := make([]string, 0, 4)
	add := func(v sql.NullString) {
		if !v.Valid {
			return
		}
		name := sanitizeFolderName(v.String)
		if name == "" {
			return
		}
		parts = append(parts, name)
	}
	add(rec.State)
	add(rec.County)
	add(rec.City)
	add(rec.Road)
	return parts
}

func sanitizeFolderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == ' ':
			return '_'
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	name = strings.Trim(name, "_.")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
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
