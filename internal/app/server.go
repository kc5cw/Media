package app

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"businessplan/usbvault/internal/audit"
	"businessplan/usbvault/internal/backup"
	"businessplan/usbvault/internal/config"
	"businessplan/usbvault/internal/db"
	"businessplan/usbvault/internal/geocode"
	"businessplan/usbvault/internal/ingest"
	"businessplan/usbvault/internal/security"
	"businessplan/usbvault/internal/usb"
)

const (
	sessionCookieName = "uv_session"
	baseStorageKey    = "base_storage_dir"
	cloudSyncKey      = "cloud_sync_config"
)

type App struct {
	store      *db.Store
	audit      *audit.Logger
	backuper   *backup.Manager
	ingestor   *ingest.Manager
	geocoder   *geocode.ReverseGeocoder
	watcher    *usb.Watcher
	logger     *log.Logger
	httpServer *http.Server
	sessionTTL time.Duration
	webDir     string
}

type contextKey string

const userKey contextKey = "user"

type AuthContext struct {
	UserID   int64
	Username string
	Token    string
}

func New(logger *log.Logger) (*App, error) {
	store, err := db.Open(config.DBPath())
	if err != nil {
		return nil, err
	}
	auditLogger := audit.New(store)
	geocoder := geocode.New(store)
	backuper := backup.NewManager(store, logger)
	ingestor := ingest.NewManager(store, auditLogger, geocoder, logger)

	application := &App{
		store:      store,
		audit:      auditLogger,
		backuper:   backuper,
		ingestor:   ingestor,
		geocoder:   geocoder,
		logger:     logger,
		sessionTTL: time.Duration(config.DefaultSessionTTLHours) * time.Hour,
		webDir:     resolveWebDir(),
	}

	interval := time.Duration(config.USBScanIntervalSeconds()) * time.Second
	application.watcher = usb.NewWatcher(interval, logger, func(mount string) {
		application.ingestor.QueueMount(mount)
	})

	return application, nil
}

func (a *App) Close() error {
	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.httpServer.Shutdown(ctx)
	}
	return a.store.Close()
}

func (a *App) Run(ctx context.Context) error {
	a.ingestor.Start(ctx)
	a.watcher.Start(ctx)

	go a.sessionCleanupWorker(ctx)
	go a.geocodeBackfillWorker(ctx)

	mux := http.NewServeMux()
	a.registerRoutes(mux)

	addr := net.JoinHostPort(defaultBindAddr(), strconv.Itoa(config.Port()))
	a.httpServer = &http.Server{
		Addr:              addr,
		Handler:           a.securityHeaders(a.requestLogger(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	a.logger.Printf("USB Vault listening on http://%s", addr)
	if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *App) geocodeBackfillWorker(ctx context.Context) {
	if !geocode.Enabled() {
		return
	}

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			todos, err := a.store.ListGeoTodos(context.Background(), 30)
			if err != nil || len(todos) == 0 {
				continue
			}
			for _, t := range todos {
				loc, err := a.geocoder.Reverse(context.Background(), t.Lat, t.Lon)
				if err != nil || loc == nil {
					continue
				}
				rec := &db.MediaRecord{
					LocProvider: toNullString(loc.Provider),
					Country:     toNullString(loc.Country),
					State:       toNullString(loc.State),
					County:      toNullString(loc.County),
					City:        toNullString(loc.City),
					Road:        toNullString(loc.Road),
					HouseNumber: toNullString(loc.HouseNumber),
					Postcode:    toNullString(loc.Postcode),
					DisplayName: toNullString(loc.DisplayName),
				}
				_ = a.store.UpdateMediaLocation(context.Background(), t.ID, rec)
			}
		}
	}
}

func (a *App) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", a.handleIndex)
	mux.Handle("GET /web/", http.StripPrefix("/web/", http.FileServer(http.Dir(a.webDir))))

	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/ingest-status", a.withAuth(a.handleIngestStatus))
	mux.HandleFunc("GET /api/backup-status", a.withAuth(a.handleBackupStatus))
	mux.HandleFunc("POST /api/setup", a.handleSetup)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)

	mux.HandleFunc("GET /api/media", a.withAuth(a.handleMediaList))
	mux.HandleFunc("GET /api/media/{id}/content", a.withAuth(a.handleMediaContent))
	mux.HandleFunc("GET /api/media/{id}/download", a.withAuth(a.handleMediaDownload))
	mux.HandleFunc("POST /api/media/download-zip", a.withAuth(a.handleMediaDownloadZip))
	mux.HandleFunc("POST /api/media/delete", a.withAuth(a.handleMediaDelete))
	mux.HandleFunc("GET /api/map", a.withAuth(a.handleMap))
	mux.HandleFunc("GET /api/location-groups", a.withAuth(a.handleLocationGroups))
	mux.HandleFunc("GET /api/audit", a.withAuth(a.handleAudit))
	mux.HandleFunc("POST /api/backup", a.withAuth(a.handleBackupStart))
	mux.HandleFunc("GET /api/mount-policy", a.withAuth(a.handleMountPolicyGet))
	mux.HandleFunc("POST /api/excluded-mounts", a.withAuth(a.handleExcludedMountsSet))
	mux.HandleFunc("POST /api/storage", a.withAuth(a.handleSetStorage))
	mux.HandleFunc("POST /api/rescan", a.withAuth(a.handleRescan))
	mux.HandleFunc("GET /api/cloud-sync", a.withAuth(a.handleCloudSyncGet))
	mux.HandleFunc("POST /api/cloud-sync", a.withAuth(a.handleCloudSyncSet))
}

func (a *App) withAuth(next func(http.ResponseWriter, *http.Request, *AuthContext)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := a.authFromRequest(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
			return
		}
		next(w, r, authCtx)
	}
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(a.webDir, "index.html")
	http.ServeFile(w, r, indexPath)
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hasUsers, err := a.store.HasUsers(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	storageDir, hasStorage, err := a.store.GetSetting(ctx, baseStorageKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	_, authed := a.authFromRequest(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"has_users":     hasUsers,
		"has_storage":   hasStorage,
		"storage_dir":   storageDir,
		"authenticated": authed,
	})
}

func (a *App) handleIngestStatus(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	writeJSON(w, http.StatusOK, a.ingestor.GetStatus())
}

func (a *App) handleBackupStatus(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	writeJSON(w, http.StatusOK, a.backuper.GetStatus())
}

type setupRequest struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	BaseStorageDir string `json:"base_storage_dir"`
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hasUsers, err := a.store.HasUsers(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	if hasUsers {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "setup already completed"})
		return
	}

	var req setupRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if !security.ValidateUsername(req.Username) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username must be 3-64 chars [a-zA-Z0-9._-]"})
		return
	}
	if err := security.ValidatePassword(req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	base := strings.TrimSpace(req.BaseStorageDir)
	if base == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_storage_dir is required"})
		return
	}
	if !filepath.IsAbs(base) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_storage_dir must be an absolute path"})
		return
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unable to create base storage directory"})
		return
	}

	hash, salt, err := security.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		return
	}
	userID, err := a.store.CreateUser(ctx, req.Username, hash, salt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
		return
	}

	if err := a.store.SetSetting(ctx, baseStorageKey, filepath.Clean(base)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save storage path"})
		return
	}

	if err := a.audit.Log(ctx, req.Username, "setup_completed", map[string]any{"storage_dir": filepath.Clean(base)}); err != nil {
		a.logger.Printf("audit error: %v", err)
	}

	if err := a.issueSession(w, userID, req.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = a.store.DeleteExpiredSessions(ctx)

	var req loginRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	user, err := a.store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	if user == nil || !security.VerifyPassword(req.Password, user.PasswordHash, user.Salt) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	if err := a.issueSession(w, user.ID, user.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		return
	}
	_ = a.audit.Log(ctx, user.Username, "login", map[string]any{"ip": clientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		tokenHash := security.TokenHash(cookie.Value)
		_ = a.store.DeleteSession(ctx, tokenHash)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleMediaList(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	size := parsePositiveInt(r.URL.Query().Get("size"), 120)
	if size > 500 {
		size = 500
	}
	offset := (page - 1) * size

	filter, err := mediaFilterFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	records, err := a.store.ListMediaFiltered(r.Context(), r.URL.Query().Get("sort"), r.URL.Query().Get("order"), size, offset, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	items := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		locationPath := buildLocationPath(rec)
		items = append(items, map[string]any{
			"id":           rec.ID,
			"kind":         rec.Kind,
			"file_name":    rec.FileName,
			"extension":    rec.Extension,
			"size_bytes":   rec.SizeBytes,
			"capture_time": rec.CaptureTime,
			"ingested_at":  rec.IngestedAt,
			"gps_lat":      nullFloat(rec.GPSLat),
			"gps_lon":      nullFloat(rec.GPSLon),
			"make":         nullString(rec.Make),
			"model":        nullString(rec.Model),
			"camera_yaw":   nullFloat(rec.CameraYaw),
			"camera_pitch": nullFloat(rec.CameraPitch),
			"camera_roll":  nullFloat(rec.CameraRoll),
			"state":        nullString(rec.State),
			"county":       nullString(rec.County),
			"city":         nullString(rec.City),
			"road":         nullString(rec.Road),
			"display_name": nullString(rec.DisplayName),
			"location":     locationPath,
			"metadata":     rec.Metadata,
			"preview_url":  fmt.Sprintf("/api/media/%d/content", rec.ID),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": page, "size": size})
}

func (a *App) handleMediaContent(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	a.serveMediaByID(w, r, false)
}

func (a *App) handleMediaDownload(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	a.serveMediaByID(w, r, true)
}

func (a *App) serveMediaByID(w http.ResponseWriter, r *http.Request, forceDownload bool) {
	idRaw := r.PathValue("id")
	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rec, err := a.store.GetMediaByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	if rec == nil {
		http.NotFound(w, r)
		return
	}

	if _, err := os.Stat(rec.DestPath); err != nil {
		http.NotFound(w, r)
		return
	}

	download := forceDownload || isTruthy(r.URL.Query().Get("download"))
	if download {
		fileName := sanitizeDownloadFilename(rec.FileName)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	http.ServeFile(w, r, rec.DestPath)
}

type mediaDeleteRequest struct {
	IDs []int64 `json:"ids"`
}

type mediaDownloadRequest struct {
	IDs []int64 `json:"ids"`
}

func (a *App) handleMediaDownloadZip(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var req mediaDownloadRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ids := normalizeIDs(req.IDs, 5000)
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids must contain at least one positive id"})
		return
	}

	records, err := a.store.ListMediaByIDs(r.Context(), ids)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	if len(records) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no matching media records"})
		return
	}

	recordByID := make(map[int64]db.MediaRecord, len(records))
	for _, rec := range records {
		recordByID[rec.ID] = rec
	}

	baseStorage, _, _ := a.store.GetSetting(r.Context(), baseStorageKey)
	baseStorage = filepath.Clean(strings.TrimSpace(baseStorage))

	zipName := fmt.Sprintf("usbvault_export_%s.zip", time.Now().UTC().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", zipName))
	w.Header().Set("Cache-Control", "private, no-store")

	zw := zip.NewWriter(w)
	defer func() {
		if err := zw.Close(); err != nil {
			a.logger.Printf("zip close error: %v", err)
		}
	}()

	written := 0
	skipped := 0
	usedNames := make(map[string]struct{}, len(records))
	for _, id := range ids {
		rec, ok := recordByID[id]
		if !ok {
			skipped++
			continue
		}

		destPath := filepath.Clean(rec.DestPath)
		if baseStorage != "." && baseStorage != "" && !config.IsPathWithin(destPath, baseStorage) {
			skipped++
			continue
		}

		info, err := os.Stat(destPath)
		if err != nil || info.IsDir() {
			skipped++
			continue
		}

		entryName := buildArchiveEntryName(rec, usedNames)
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			skipped++
			continue
		}
		hdr.Name = entryName
		hdr.Method = zip.Deflate

		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			skipped++
			continue
		}

		src, err := os.Open(destPath)
		if err != nil {
			skipped++
			continue
		}
		_, copyErr := io.Copy(dst, src)
		_ = src.Close()
		if copyErr != nil {
			skipped++
			continue
		}
		written++
	}

	_ = a.audit.Log(r.Context(), authCtx.Username, "media_download_zip", map[string]any{
		"requested": len(ids),
		"written":   written,
		"skipped":   skipped,
	})
}

func (a *App) handleMediaDelete(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var req mediaDeleteRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ids := normalizeIDs(req.IDs, 5000)
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids must contain at least one positive id"})
		return
	}

	records, err := a.store.ListMediaByIDs(r.Context(), ids)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	recordByID := make(map[int64]db.MediaRecord, len(records))
	for _, rec := range records {
		recordByID[rec.ID] = rec
	}

	baseStorage, _, _ := a.store.GetSetting(r.Context(), baseStorageKey)
	baseStorage = filepath.Clean(strings.TrimSpace(baseStorage))

	deleted := 0
	notFound := 0
	failed := 0
	for _, id := range ids {
		rec, ok := recordByID[id]
		if !ok {
			notFound++
			continue
		}

		destPath := filepath.Clean(rec.DestPath)
		if baseStorage != "." && baseStorage != "" && !config.IsPathWithin(destPath, baseStorage) {
			failed++
			continue
		}

		if err := os.Remove(destPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed++
			continue
		}
		if err := a.store.DeleteMediaByID(r.Context(), id); err != nil {
			failed++
			continue
		}
		cleanupEmptyParents(destPath, baseStorage)
		deleted++
	}

	_ = a.audit.Log(r.Context(), authCtx.Username, "media_deleted", map[string]any{
		"requested": len(ids),
		"deleted":   deleted,
		"not_found": notFound,
		"failed":    failed,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"requested": len(ids),
		"deleted":   deleted,
		"not_found": notFound,
		"failed":    failed,
	})
}

func (a *App) handleMap(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	filter, err := mediaFilterFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	points, err := a.store.ListMapPointsFiltered(r.Context(), 2000, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": points})
}

func (a *App) handleLocationGroups(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	level := r.URL.Query().Get("level")
	filter, err := mediaFilterFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	groups, err := a.store.ListLocationGroups(r.Context(), level, filter, 200)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": level, "groups": groups})
}

func (a *App) handleAudit(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	records, err := a.store.ListAudit(r.Context(), 300)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": records, "viewer": authCtx.Username})
}

func (a *App) handleMountPolicyGet(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	ctx := r.Context()

	excluded, err := a.getExcludedMounts(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}

	baseStorage, hasStorage, err := a.store.GetSetting(ctx, baseStorageKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	if hasStorage {
		baseStorage = filepath.Clean(baseStorage)
	}

	mounts := a.watcher.CurrentMounts()
	autoExcluded := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		if hasStorage && config.IsPathWithin(baseStorage, mount) {
			autoExcluded = append(autoExcluded, mount)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mounts":               mounts,
		"excluded_mounts":      excluded,
		"auto_excluded_mounts": autoExcluded,
		"storage_dir":          baseStorage,
	})
}

type excludedMountsRequest struct {
	Mounts []string `json:"mounts"`
}

func (a *App) handleExcludedMountsSet(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var req excludedMountsRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	normalized := config.NormalizeAbsolutePaths(req.Mounts)
	if len(normalized) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many excluded mounts"})
		return
	}

	if err := a.store.SetSetting(r.Context(), config.ExcludedMountsSettingKey, config.EncodePathList(normalized)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update excluded mounts"})
		return
	}
	_ = a.audit.Log(r.Context(), authCtx.Username, "excluded_mounts_updated", map[string]any{"count": len(normalized)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "excluded_mounts": normalized})
}

type storageRequest struct {
	BaseStorageDir string `json:"base_storage_dir"`
}

func (a *App) handleSetStorage(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var req storageRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	base := strings.TrimSpace(req.BaseStorageDir)
	if base == "" || !filepath.IsAbs(base) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_storage_dir must be an absolute path"})
		return
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unable to create base storage directory"})
		return
	}

	if err := a.store.SetSetting(r.Context(), baseStorageKey, filepath.Clean(base)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update storage"})
		return
	}
	_ = a.audit.Log(r.Context(), authCtx.Username, "storage_updated", map[string]any{"storage_dir": filepath.Clean(base)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type rescanRequest struct {
	MountPath string `json:"mount_path"`
}

func (a *App) handleRescan(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var req rescanRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	mount := strings.TrimSpace(req.MountPath)
	if mount == "" || !filepath.IsAbs(mount) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mount_path must be an absolute path"})
		return
	}

	res, err := a.ingestor.ProcessMount(r.Context(), mount, authCtx.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (a *App) handleCloudSyncGet(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	value, ok, err := a.store.GetSetting(r.Context(), cloudSyncKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	if !ok {
		value = `{"enabled":false,"provider":"none","rules":[]}`
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		payload = map[string]any{"enabled": false, "provider": "none", "rules": []any{}}
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) handleCloudSyncSet(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var payload map[string]any
	if err := decodeJSONBody(r, &payload, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if err := a.store.SetSetting(r.Context(), cloudSyncKey, string(raw)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update cloud sync settings"})
		return
	}
	_ = a.audit.Log(r.Context(), authCtx.Username, "cloud_sync_config_updated", map[string]any{"length": len(raw)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type backupStartRequest struct {
	Mode        string `json:"mode"`
	Destination string `json:"destination"`
	SSHPort     int    `json:"ssh_port"`
	APIMethod   string `json:"api_method"`
	APIToken    string `json:"api_token"`
}

func (a *App) handleBackupStart(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	var req backupStartRequest
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	err := a.backuper.Start(authCtx.Username, backup.Request{
		Mode:        req.Mode,
		Destination: req.Destination,
		SSHPort:     req.SSHPort,
		APIMethod:   req.APIMethod,
		APIToken:    req.APIToken,
	})
	if err != nil {
		if errors.Is(err, backup.ErrBusy) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, backup.ErrInvalidRequest) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = a.audit.Log(r.Context(), authCtx.Username, "backup_started", map[string]any{
		"mode":        req.Mode,
		"destination": req.Destination,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *App) issueSession(w http.ResponseWriter, userID int64, username string) error {
	token, err := security.NewSessionToken()
	if err != nil {
		return err
	}
	tokenHash := security.TokenHash(token)
	expires := time.Now().UTC().Add(a.sessionTTL)
	if err := a.store.CreateSession(context.Background(), tokenHash, userID, expires); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		Secure:   false,
	})
	_ = username
	return nil
}

func (a *App) authFromRequest(r *http.Request) (*AuthContext, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	tokenHash := security.TokenHash(cookie.Value)
	session, err := a.store.LookupSession(r.Context(), tokenHash)
	if err != nil || session == nil {
		return nil, false
	}
	return &AuthContext{UserID: session.UserID, Username: session.Username, Token: cookie.Value}, true
}

func (a *App) sessionCleanupWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.store.DeleteExpiredSessions(context.Background()); err != nil {
				a.logger.Printf("session cleanup failed: %v", err)
			}
		}
	}
}

func (a *App) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		a.logger.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		// Leaflet is vendored under /web/vendor for offline use; map tiles may still be remote.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://tile.openstreetmap.org; style-src 'self' 'unsafe-inline'; script-src 'self'; font-src 'self' data:; connect-src 'self'; media-src 'self'; frame-ancestors 'none';")
		next.ServeHTTP(w, r)
	})
}

func defaultBindAddr() string {
	if v := strings.TrimSpace(os.Getenv("USBVAULT_BIND")); v != "" {
		return v
	}
	return "127.0.0.1"
}

func resolveWebDir() string {
	if raw := strings.TrimSpace(os.Getenv("USBVAULT_WEB_DIR")); raw != "" {
		return raw
	}
	return filepath.Join("web")
}

func decodeJSONBody(r *http.Request, out any, maxBytes int64) error {
	defer r.Body.Close()

	limited := io.LimitReader(r.Body, maxBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return errors.New("empty JSON payload")
	}

	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("invalid JSON payload")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func parsePositiveInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func nullFloat(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}

func nullString(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func toNullString(v string) sql.NullString {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func buildLocationPath(rec db.MediaRecord) string {
	parts := make([]string, 0, 4)
	if rec.State.Valid && strings.TrimSpace(rec.State.String) != "" {
		parts = append(parts, strings.TrimSpace(rec.State.String))
	}
	if rec.County.Valid && strings.TrimSpace(rec.County.String) != "" {
		parts = append(parts, strings.TrimSpace(rec.County.String))
	}
	if rec.City.Valid && strings.TrimSpace(rec.City.String) != "" {
		parts = append(parts, strings.TrimSpace(rec.City.String))
	}
	if rec.Road.Valid && strings.TrimSpace(rec.Road.String) != "" {
		parts = append(parts, strings.TrimSpace(rec.Road.String))
	}
	if len(parts) == 0 {
		return "Unknown"
	}
	return strings.Join(parts, " / ")
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) getExcludedMounts(ctx context.Context) ([]string, error) {
	raw, _, err := a.store.GetSetting(ctx, config.ExcludedMountsSettingKey)
	if err != nil {
		return nil, err
	}
	return config.ParsePathList(raw), nil
}

func mediaFilterFromRequest(r *http.Request) (db.MediaFilter, error) {
	filter := db.MediaFilter{
		State:  strings.TrimSpace(r.URL.Query().Get("state")),
		County: strings.TrimSpace(r.URL.Query().Get("county")),
		City:   strings.TrimSpace(r.URL.Query().Get("city")),
		Road:   strings.TrimSpace(r.URL.Query().Get("road")),
		Kind:   strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind"))),
		Query:  strings.TrimSpace(r.URL.Query().Get("q")),
		HasGPS: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("gps"))),
	}
	if filter.Kind != "" && filter.Kind != "image" && filter.Kind != "video" {
		return db.MediaFilter{}, errors.New("invalid kind filter")
	}
	if filter.HasGPS != "" && filter.HasGPS != "yes" && filter.HasGPS != "no" {
		return db.MediaFilter{}, errors.New("invalid gps filter")
	}

	from, err := normalizeFilterTime(r.URL.Query().Get("from"), false)
	if err != nil {
		return db.MediaFilter{}, errors.New("invalid from date")
	}
	to, err := normalizeFilterTime(r.URL.Query().Get("to"), true)
	if err != nil {
		return db.MediaFilter{}, errors.New("invalid to date")
	}
	if from != "" && to != "" && from > to {
		return db.MediaFilter{}, errors.New("from date must be before to date")
	}
	filter.CaptureFrom = from
	filter.CaptureTo = to
	return filter, nil
}

func normalizeFilterTime(raw string, endOfDay bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err != nil {
			continue
		}
		if layout == "2006-01-02" {
			if endOfDay {
				parsed = parsed.Add(24*time.Hour - time.Second)
			}
		}
		return parsed.UTC().Format(time.RFC3339), nil
	}
	return "", errors.New("invalid datetime")
}

func isTruthy(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func sanitizeDownloadFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "media.bin"
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\"", "_")
	name = strings.ReplaceAll(name, "\r", "_")
	name = strings.ReplaceAll(name, "\n", "_")
	return name
}

func buildArchiveEntryName(rec db.MediaRecord, used map[string]struct{}) string {
	parts := make([]string, 0, 8)
	add := func(v sql.NullString) {
		if !v.Valid {
			return
		}
		name := sanitizeArchiveSegment(v.String)
		if name == "" {
			return
		}
		parts = append(parts, name)
	}
	add(rec.State)
	add(rec.County)
	add(rec.City)
	add(rec.Road)
	if len(parts) == 0 {
		parts = append(parts, "Unknown")
	}

	if tm, err := time.Parse(time.RFC3339, rec.CaptureTime); err == nil {
		parts = append(parts, tm.UTC().Format("2006"), tm.UTC().Format("01"), tm.UTC().Format("02"))
	}

	baseName := sanitizeDownloadFilename(rec.FileName)
	if baseName == "" {
		baseName = fmt.Sprintf("media_%d%s", rec.ID, rec.Extension)
	}
	baseName = fmt.Sprintf("%06d_%s", rec.ID, baseName)

	candidate := path.Join(append(parts, baseName)...)
	if _, ok := used[candidate]; !ok {
		used[candidate] = struct{}{}
		return candidate
	}

	ext := strings.ToLower(filepath.Ext(baseName))
	stem := strings.TrimSuffix(baseName, ext)
	for i := 1; i <= 10000; i++ {
		alt := path.Join(append(parts, fmt.Sprintf("%s_%d%s", stem, i, ext))...)
		if _, ok := used[alt]; ok {
			continue
		}
		used[alt] = struct{}{}
		return alt
	}
	fallback := path.Join(append(parts, fmt.Sprintf("%d_%d%s", rec.ID, time.Now().UnixNano(), ext))...)
	used[fallback] = struct{}{}
	return fallback
}

func sanitizeArchiveSegment(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.', r == ' ':
			return r
		default:
			return '_'
		}
	}, name)
	name = strings.Trim(name, "_. ")
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}

func normalizeIDs(ids []int64, max int) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= max {
			break
		}
	}
	return out
}

func cleanupEmptyParents(filePath, stopDir string) {
	stopDir = strings.TrimSpace(stopDir)
	if stopDir == "" {
		return
	}
	stopDir = filepath.Clean(stopDir)
	dir := filepath.Dir(filePath)
	for {
		if !config.IsPathWithin(dir, stopDir) || config.PathKey(dir) == config.PathKey(stopDir) {
			return
		}
		err := os.Remove(dir)
		if err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
