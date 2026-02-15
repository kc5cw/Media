package app

import (
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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"businessplan/usbvault/internal/audit"
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
	ingestor := ingest.NewManager(store, auditLogger, geocoder, logger)

	application := &App{
		store:      store,
		audit:      auditLogger,
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
		WriteTimeout:      120 * time.Second,
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
	mux.HandleFunc("POST /api/setup", a.handleSetup)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)

	mux.HandleFunc("GET /api/media", a.withAuth(a.handleMediaList))
	mux.HandleFunc("GET /api/media/{id}/content", a.withAuth(a.handleMediaContent))
	mux.HandleFunc("GET /api/map", a.withAuth(a.handleMap))
	mux.HandleFunc("GET /api/location-groups", a.withAuth(a.handleLocationGroups))
	mux.HandleFunc("GET /api/audit", a.withAuth(a.handleAudit))
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

	filter := db.MediaFilter{
		State:  r.URL.Query().Get("state"),
		County: r.URL.Query().Get("county"),
		City:   r.URL.Query().Get("city"),
		Road:   r.URL.Query().Get("road"),
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

	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, rec.DestPath)
}

func (a *App) handleMap(w http.ResponseWriter, r *http.Request, authCtx *AuthContext) {
	_ = authCtx
	filter := db.MediaFilter{
		State:  r.URL.Query().Get("state"),
		County: r.URL.Query().Get("county"),
		City:   r.URL.Query().Get("city"),
		Road:   r.URL.Query().Get("road"),
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
	filter := db.MediaFilter{
		State:  r.URL.Query().Get("state"),
		County: r.URL.Query().Get("county"),
		City:   r.URL.Query().Get("city"),
		Road:   r.URL.Query().Get("road"),
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://tile.openstreetmap.org; style-src 'self' 'unsafe-inline' https://unpkg.com; script-src 'self' https://unpkg.com; font-src 'self' data:; connect-src 'self'; media-src 'self'; frame-ancestors 'none';")
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
