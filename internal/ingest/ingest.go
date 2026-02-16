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

	statusMu sync.Mutex
	status   Status

	rateMu      sync.Mutex
	rateSamples []rateSample
}

type rateSample struct {
	At    time.Time
	Bytes int64
}

type Result struct {
	Scanned    int `json:"scanned"`
	Copied     int `json:"copied"`
	Duplicates int `json:"duplicates"`
	Errors     int `json:"errors"`
}

type Status struct {
	State          string  `json:"state"` // idle, scanning, ingesting, error
	Mount          string  `json:"mount"`
	Phase          string  `json:"phase"` // scan, ingest
	StartedAt      string  `json:"started_at"`
	UpdatedAt      string  `json:"updated_at"`
	TotalFiles     int     `json:"total_files"`
	ProcessedFiles int     `json:"processed_files"`
	CopiedFiles    int     `json:"copied_files"`
	Duplicates     int     `json:"duplicates"`
	Errors         int     `json:"errors"`
	TotalBytes     int64   `json:"total_bytes"`
	CopiedBytes    int64   `json:"copied_bytes"`
	Percent        float64 `json:"percent"`
	FilesPerSec    float64 `json:"files_per_sec"`
	MBps           float64 `json:"mbps"`
	CurrentPath    string  `json:"current_path"`
	Message        string  `json:"message"`
	LastResult     Result  `json:"last_result"`
}

func NewManager(store *db.Store, auditLogger *audit.Logger, geocoder *geocode.ReverseGeocoder, logger *log.Logger) *Manager {
	m := &Manager{
		store:    store,
		audit:    auditLogger,
		geocoder: geocoder,
		logger:   logger,
		jobs:     make(chan string, 16),
	}
	m.status = Status{State: "idle"}
	return m
}

func (m *Manager) GetStatus() Status {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()

	st := m.status
	// Derive rates and percent.
	if st.StartedAt != "" {
		if started, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil {
			elapsed := time.Since(started).Seconds()
			if elapsed > 0 {
				st.FilesPerSec = float64(st.ProcessedFiles) / elapsed
			}
		}
	}
	if st.State == "ingesting" {
		st.MBps = m.currentMBps()
	} else {
		st.MBps = 0
	}
	if st.TotalFiles > 0 {
		st.Percent = (float64(st.ProcessedFiles) / float64(st.TotalFiles)) * 100.0
		if st.Percent > 100 {
			st.Percent = 100
		}
	}
	return st
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

	excludedRaw, _, err := m.store.GetSetting(ctx, config.ExcludedMountsSettingKey)
	if err != nil {
		return result, err
	}
	excludedMounts := config.ParsePathList(excludedRaw)

	if shouldSkipMount(mountPath, baseStorage, excludedMounts) {
		_ = m.audit.Log(ctx, actor, "ingest_skipped_excluded_mount", map[string]any{
			"mount":           mountPath,
			"base_storage":    baseStorage,
			"excluded_mounts": excludedMounts,
		})
		return result, nil
	}

	layout := storageLayoutLocationDate
	if raw, ok, err := m.store.GetSetting(ctx, storageLayoutSetting); err == nil && ok {
		layout = normalizeStorageLayout(raw)
	}

	m.setStatus(Status{
		State:     "scanning",
		Mount:     mountPath,
		Phase:     "scan",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Message:   "Scanning for media...",
	})
	m.resetRateSamples()

	_ = m.audit.Log(ctx, actor, "ingest_started", map[string]any{"mount": mountPath})

	// First pass: count supported files and total bytes for percent/rate reporting.
	var totalFiles int
	var totalBytes int64
	scanErr := filepath.WalkDir(mountPath, func(path string, d fs.DirEntry, walkErr error) error {
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
		_ = kind
		result.Scanned++
		totalFiles++
		if info, err := os.Stat(path); err == nil {
			totalBytes += info.Size()
		}
		if totalFiles%50 == 0 {
			m.bumpStatus(func(st *Status) {
				st.TotalFiles = totalFiles
				st.TotalBytes = totalBytes
				st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			})
		}
		return nil
	})
	if scanErr != nil {
		m.bumpStatus(func(st *Status) {
			st.State = "error"
			st.Message = "Scan failed"
			st.Errors++
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		})
		return result, scanErr
	}

	m.bumpStatus(func(st *Status) {
		st.State = "ingesting"
		st.Phase = "ingest"
		st.TotalFiles = totalFiles
		st.TotalBytes = totalBytes
		st.Message = "Ingesting media..."
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	})

	// Second pass: ingest.
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

		m.bumpStatus(func(st *Status) {
			st.CurrentPath = path
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		})

		if err := m.ingestFile(ctx, mountPath, baseStorage, layout, path, kind, actor, &result); err != nil {
			result.Errors++
			m.logger.Printf("ingest file error %s: %v", path, err)
		}

		m.bumpStatus(func(st *Status) {
			st.ProcessedFiles++
			st.CopiedFiles = result.Copied
			st.Duplicates = result.Duplicates
			st.Errors = result.Errors
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		})

		return nil
	})

	if walkErr != nil {
		m.bumpStatus(func(st *Status) {
			st.State = "error"
			st.Message = "Ingest failed"
			st.Errors++
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		})
		return result, walkErr
	}

	_ = m.audit.Log(ctx, actor, "ingest_completed", map[string]any{
		"mount":      mountPath,
		"scanned":    result.Scanned,
		"copied":     result.Copied,
		"duplicates": result.Duplicates,
		"errors":     result.Errors,
	})

	m.setStatus(Status{
		State:      "idle",
		Mount:      "",
		Phase:      "",
		StartedAt:  "",
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Message:    "Idle",
		LastResult: result,
	})
	return result, nil
}

func shouldSkipMount(mountPath, baseStorage string, excludedMounts []string) bool {
	// Never ingest from the destination storage drive/mount itself.
	if config.IsPathWithin(baseStorage, mountPath) || config.IsPathWithin(mountPath, baseStorage) {
		return true
	}

	// User-managed exclusions.
	for _, excluded := range excludedMounts {
		if config.IsPathWithin(mountPath, excluded) || config.IsPathWithin(excluded, mountPath) {
			return true
		}
	}
	return false
}

func (m *Manager) setStatus(st Status) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.status = st
}

func (m *Manager) bumpStatus(update func(st *Status)) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	update(&m.status)
}

func (m *Manager) addCopiedBytes(delta int64) {
	if delta == 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	m.bumpStatus(func(st *Status) {
		st.CopiedBytes += delta
		if st.CopiedBytes < 0 {
			st.CopiedBytes = 0
		}
		st.UpdatedAt = now
	})
	if delta > 0 {
		m.recordRateSample(delta)
	}
}

func (m *Manager) resetRateSamples() {
	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	m.rateSamples = nil
}

func (m *Manager) recordRateSample(bytes int64) {
	if bytes <= 0 {
		return
	}

	now := time.Now()
	cutoff := now.Add(-10 * time.Second)

	m.rateMu.Lock()
	defer m.rateMu.Unlock()

	m.rateSamples = append(m.rateSamples, rateSample{At: now, Bytes: bytes})
	trim := 0
	for trim < len(m.rateSamples) && m.rateSamples[trim].At.Before(cutoff) {
		trim++
	}
	if trim > 0 {
		m.rateSamples = append([]rateSample(nil), m.rateSamples[trim:]...)
	}
}

func (m *Manager) currentMBps() float64 {
	now := time.Now()
	recentCutoff := now.Add(-3 * time.Second)
	keepCutoff := now.Add(-10 * time.Second)

	m.rateMu.Lock()
	defer m.rateMu.Unlock()

	trim := 0
	for trim < len(m.rateSamples) && m.rateSamples[trim].At.Before(keepCutoff) {
		trim++
	}
	if trim > 0 {
		m.rateSamples = append([]rateSample(nil), m.rateSamples[trim:]...)
	}

	var bytes int64
	var first time.Time
	for _, sample := range m.rateSamples {
		if sample.At.Before(recentCutoff) {
			continue
		}
		if first.IsZero() {
			first = sample.At
		}
		bytes += sample.Bytes
	}
	if bytes <= 0 {
		return 0
	}

	elapsed := now.Sub(first).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	return (float64(bytes) / elapsed) / (1024.0 * 1024.0)
}

func (m *Manager) ingestFile(ctx context.Context, mountPath, baseStorage, layout, srcPath, kind, actor string, result *Result) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	crcHex, shaHex, err := media.ComputeHashesWithProgress(srcPath, func(n int64) {
		m.recordRateSample(n)
	})
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
	var copiedThisFile int64
	if err := copyFileAtomic(srcPath, destPath, info.Mode(), info.ModTime(), func(n int64) {
		copiedThisFile += n
		m.addCopiedBytes(n)
	}); err != nil {
		if copiedThisFile > 0 {
			m.addCopiedBytes(-copiedThisFile)
		}
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

func copyFileAtomic(srcPath, dstPath string, srcMode fs.FileMode, modTime time.Time, onProgress func(int64)) error {
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
		copySrc := io.Reader(src)
		if onProgress != nil {
			copySrc = &progressReader{r: src, onProgress: onProgress}
		}
		buf := make([]byte, 1024*1024)
		if _, err := io.CopyBuffer(dst, copySrc, buf); err != nil {
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

type progressReader struct {
	r          io.Reader
	onProgress func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.onProgress != nil {
		p.onProgress(int64(n))
	}
	return n, err
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
