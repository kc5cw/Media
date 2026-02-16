package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
	mu sync.Mutex
}

type User struct {
	ID           int64
	Username     string
	PasswordHash []byte
	Salt         []byte
}

type Session struct {
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

type MediaRecord struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	FileName    string          `json:"file_name"`
	Extension   string          `json:"extension"`
	SourceMount string          `json:"source_mount"`
	SourcePath  string          `json:"source_path"`
	DestPath    string          `json:"dest_path"`
	SizeBytes   int64           `json:"size_bytes"`
	CRC32       string          `json:"crc32"`
	SHA256      string          `json:"sha256"`
	CaptureTime string          `json:"capture_time"`
	GPSLat      sql.NullFloat64 `json:"gps_lat"`
	GPSLon      sql.NullFloat64 `json:"gps_lon"`
	Make        sql.NullString  `json:"make"`
	Model       sql.NullString  `json:"model"`
	CameraYaw   sql.NullFloat64 `json:"camera_yaw"`
	CameraPitch sql.NullFloat64 `json:"camera_pitch"`
	CameraRoll  sql.NullFloat64 `json:"camera_roll"`
	LocProvider sql.NullString  `json:"loc_provider"`
	Country     sql.NullString  `json:"country"`
	State       sql.NullString  `json:"state"`
	County      sql.NullString  `json:"county"`
	City        sql.NullString  `json:"city"`
	Road        sql.NullString  `json:"road"`
	HouseNumber sql.NullString  `json:"house_number"`
	Postcode    sql.NullString  `json:"postcode"`
	DisplayName sql.NullString  `json:"display_name"`
	Metadata    string          `json:"metadata"`
	SourceMTime string          `json:"source_mtime"`
	IngestedAt  string          `json:"ingested_at"`
}

type MapPoint struct {
	ID          int64   `json:"id"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	CaptureTime string  `json:"capture_time"`
	FileName    string  `json:"file_name"`
	Kind        string  `json:"kind"`
}

type AuditRecord struct {
	ID      int64  `json:"id"`
	TS      string `json:"ts"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Details string `json:"details"`
	Hash    string `json:"hash"`
}

type GeocodeCacheEntry struct {
	Provider    string
	GeocodeKey  string
	Country     string
	State       string
	County      string
	City        string
	Road        string
	HouseNumber string
	Postcode    string
	DisplayName string
	RawJSON     string
	UpdatedAt   string
}

type MediaFilter struct {
	State  string
	County string
	City   string
	Road   string
}

type LocationGroup struct {
	Name   string          `json:"name"`
	Count  int64           `json:"count"`
	MinLat sql.NullFloat64 `json:"min_lat"`
	MinLon sql.NullFloat64 `json:"min_lon"`
	MaxLat sql.NullFloat64 `json:"max_lat"`
	MaxLon sql.NullFloat64 `json:"max_lon"`
}

type GeoTodo struct {
	ID  int64
	Lat float64
	Lon float64
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetConnMaxIdleTime(1 * time.Minute)
	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(1)

	store := &Store{DB: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	schema := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL,
			salt BLOB NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS media_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			file_name TEXT NOT NULL,
			extension TEXT NOT NULL,
			source_mount TEXT NOT NULL,
			source_path TEXT NOT NULL,
			dest_path TEXT NOT NULL UNIQUE,
			size_bytes INTEGER NOT NULL,
			crc32 TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			capture_time TEXT NOT NULL,
			gps_lat REAL,
			gps_lon REAL,
			make TEXT,
			model TEXT,
			camera_yaw REAL,
			camera_pitch REAL,
			camera_roll REAL,
			loc_provider TEXT,
			loc_country TEXT,
			loc_state TEXT,
			loc_county TEXT,
			loc_city TEXT,
			loc_road TEXT,
			loc_house_number TEXT,
			loc_postcode TEXT,
			loc_display_name TEXT,
			metadata_json TEXT NOT NULL,
			source_mtime TEXT NOT NULL,
			ingested_at TEXT NOT NULL,
			UNIQUE (crc32, size_bytes, capture_time)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_media_capture_time ON media_files(capture_time);`,
		`CREATE INDEX IF NOT EXISTS idx_media_gps ON media_files(gps_lat, gps_lon);`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			details_json TEXT NOT NULL,
			prev_hash TEXT NOT NULL,
			entry_hash TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS cloud_sync_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			enabled INTEGER NOT NULL DEFAULT 0,
			provider TEXT NOT NULL DEFAULT '',
			config_json TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS geocode_cache (
			provider TEXT NOT NULL,
			geocode_key TEXT NOT NULL,
			country TEXT,
			state TEXT,
			county TEXT,
			city TEXT,
			road TEXT,
			house_number TEXT,
			postcode TEXT,
			display_name TEXT,
			raw_json TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (provider, geocode_key)
		);`,
	}

	for _, stmt := range schema {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("run migration: %w", err)
		}
	}

	// For DBs created before we added location columns.
	if err := s.ensureMediaLocationColumns(ctx); err != nil {
		return err
	}

	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_media_loc_state ON media_files(loc_state);`); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_media_loc_county ON media_files(loc_county);`); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_media_loc_city ON media_files(loc_city);`); err != nil {
		return err
	}

	return nil
}

func (s *Store) ensureMediaLocationColumns(ctx context.Context) error {
	existing := map[string]struct{}{}
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(media_files)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	cols := []struct {
		name    string
		typeDef string
	}{
		{"loc_provider", "TEXT"},
		{"loc_country", "TEXT"},
		{"loc_state", "TEXT"},
		{"loc_county", "TEXT"},
		{"loc_city", "TEXT"},
		{"loc_road", "TEXT"},
		{"loc_house_number", "TEXT"},
		{"loc_postcode", "TEXT"},
		{"loc_display_name", "TEXT"},
	}

	for _, col := range cols {
		if _, ok := existing[col.name]; ok {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE media_files ADD COLUMN %s %s", col.name, col.typeDef)
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key)
	var value string
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now,
	)
	return err
}

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM users`)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) CreateUser(ctx context.Context, username string, hash, salt []byte) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, salt, created_at) VALUES (?, ?, ?, ?)`,
		username, hash, salt, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, salt FROM users WHERE username = ?`,
		username,
	)
	var user User
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Salt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, expiresAt.UTC().Format(time.RFC3339), now,
	)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}

func (s *Store) LookupSession(ctx context.Context, tokenHash string) (*Session, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT s.user_id, u.username, s.expires_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?`,
		tokenHash,
	)
	var (
		session   Session
		expiresAt string
	)
	if err := row.Scan(&session.UserID, &session.Username, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return nil, err
	}
	session.ExpiresAt = parsed
	if time.Now().UTC().After(parsed) {
		_ = s.DeleteSession(ctx, tokenHash)
		return nil, nil
	}
	return &session, nil
}

func (s *Store) MediaExists(ctx context.Context, crc32 string, size int64, captureTime string) (bool, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM media_files WHERE crc32 = ? AND size_bytes = ? AND capture_time = ? LIMIT 1`,
		crc32, size, captureTime,
	)
	var marker int
	if err := row.Scan(&marker); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) InsertMedia(ctx context.Context, rec *MediaRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO media_files (
				kind, file_name, extension, source_mount, source_path, dest_path,
				size_bytes, crc32, sha256, capture_time, gps_lat, gps_lon, make, model,
				camera_yaw, camera_pitch, camera_roll,
				loc_provider, loc_country, loc_state, loc_county, loc_city, loc_road, loc_house_number, loc_postcode, loc_display_name,
				metadata_json, source_mtime, ingested_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.Kind,
		rec.FileName,
		rec.Extension,
		filepath.Clean(rec.SourceMount),
		rec.SourcePath,
		rec.DestPath,
		rec.SizeBytes,
		rec.CRC32,
		rec.SHA256,
		rec.CaptureTime,
		nullFloatToAny(rec.GPSLat),
		nullFloatToAny(rec.GPSLon),
		nullStringToAny(rec.Make),
		nullStringToAny(rec.Model),
		nullFloatToAny(rec.CameraYaw),
		nullFloatToAny(rec.CameraPitch),
		nullFloatToAny(rec.CameraRoll),
		nullStringToAny(rec.LocProvider),
		nullStringToAny(rec.Country),
		nullStringToAny(rec.State),
		nullStringToAny(rec.County),
		nullStringToAny(rec.City),
		nullStringToAny(rec.Road),
		nullStringToAny(rec.HouseNumber),
		nullStringToAny(rec.Postcode),
		nullStringToAny(rec.DisplayName),
		rec.Metadata,
		rec.SourceMTime,
		rec.IngestedAt,
	)
	return err
}

func (s *Store) ListMedia(ctx context.Context, sortBy, order string, limit, offset int) ([]MediaRecord, error) {
	return s.ListMediaFiltered(ctx, sortBy, order, limit, offset, MediaFilter{})
}

func (s *Store) ListMediaFiltered(ctx context.Context, sortBy, order string, limit, offset int, filter MediaFilter) ([]MediaRecord, error) {
	safeSort := "capture_time"
	switch sortBy {
	case "capture_time", "ingested_at", "file_name", "size_bytes", "kind":
		safeSort = sortBy
	}
	safeOrder := "DESC"
	if strings.EqualFold(order, "asc") {
		safeOrder = "ASC"
	}

	where, args := buildLocationWhere(filter)

	query := fmt.Sprintf(`
		SELECT id, kind, file_name, extension, source_mount, source_path, dest_path, size_bytes, crc32, sha256,
		       capture_time, gps_lat, gps_lon, make, model, camera_yaw, camera_pitch, camera_roll,
		       loc_provider, loc_country, loc_state, loc_county, loc_city, loc_road, loc_house_number, loc_postcode, loc_display_name,
		       metadata_json, source_mtime, ingested_at
		FROM media_files
		WHERE %s
		ORDER BY %s %s
		LIMIT ? OFFSET ?
	`, where, safeSort, safeOrder)

	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MediaRecord, 0)
	for rows.Next() {
		var rec MediaRecord
		if err := rows.Scan(
			&rec.ID,
			&rec.Kind,
			&rec.FileName,
			&rec.Extension,
			&rec.SourceMount,
			&rec.SourcePath,
			&rec.DestPath,
			&rec.SizeBytes,
			&rec.CRC32,
			&rec.SHA256,
			&rec.CaptureTime,
			&rec.GPSLat,
			&rec.GPSLon,
			&rec.Make,
			&rec.Model,
			&rec.CameraYaw,
			&rec.CameraPitch,
			&rec.CameraRoll,
			&rec.LocProvider,
			&rec.Country,
			&rec.State,
			&rec.County,
			&rec.City,
			&rec.Road,
			&rec.HouseNumber,
			&rec.Postcode,
			&rec.DisplayName,
			&rec.Metadata,
			&rec.SourceMTime,
			&rec.IngestedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}

	return out, rows.Err()
}

func (s *Store) GetMediaByID(ctx context.Context, id int64) (*MediaRecord, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, kind, file_name, extension, source_mount, source_path, dest_path, size_bytes, crc32, sha256,
		       capture_time, gps_lat, gps_lon, make, model, camera_yaw, camera_pitch, camera_roll,
		       loc_provider, loc_country, loc_state, loc_county, loc_city, loc_road, loc_house_number, loc_postcode, loc_display_name,
		       metadata_json, source_mtime, ingested_at
		FROM media_files WHERE id = ?
	`, id)
	var rec MediaRecord
	if err := row.Scan(
		&rec.ID,
		&rec.Kind,
		&rec.FileName,
		&rec.Extension,
		&rec.SourceMount,
		&rec.SourcePath,
		&rec.DestPath,
		&rec.SizeBytes,
		&rec.CRC32,
		&rec.SHA256,
		&rec.CaptureTime,
		&rec.GPSLat,
		&rec.GPSLon,
		&rec.Make,
		&rec.Model,
		&rec.CameraYaw,
		&rec.CameraPitch,
		&rec.CameraRoll,
		&rec.LocProvider,
		&rec.Country,
		&rec.State,
		&rec.County,
		&rec.City,
		&rec.Road,
		&rec.HouseNumber,
		&rec.Postcode,
		&rec.DisplayName,
		&rec.Metadata,
		&rec.SourceMTime,
		&rec.IngestedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (s *Store) ListMediaByIDs(ctx context.Context, ids []int64) ([]MediaRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT id, kind, file_name, extension, source_mount, source_path, dest_path, size_bytes, crc32, sha256,
		       capture_time, gps_lat, gps_lon, make, model, camera_yaw, camera_pitch, camera_roll,
		       loc_provider, loc_country, loc_state, loc_county, loc_city, loc_road, loc_house_number, loc_postcode, loc_display_name,
		       metadata_json, source_mtime, ingested_at
		FROM media_files
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MediaRecord, 0, len(ids))
	for rows.Next() {
		var rec MediaRecord
		if err := rows.Scan(
			&rec.ID,
			&rec.Kind,
			&rec.FileName,
			&rec.Extension,
			&rec.SourceMount,
			&rec.SourcePath,
			&rec.DestPath,
			&rec.SizeBytes,
			&rec.CRC32,
			&rec.SHA256,
			&rec.CaptureTime,
			&rec.GPSLat,
			&rec.GPSLon,
			&rec.Make,
			&rec.Model,
			&rec.CameraYaw,
			&rec.CameraPitch,
			&rec.CameraRoll,
			&rec.LocProvider,
			&rec.Country,
			&rec.State,
			&rec.County,
			&rec.City,
			&rec.Road,
			&rec.HouseNumber,
			&rec.Postcode,
			&rec.DisplayName,
			&rec.Metadata,
			&rec.SourceMTime,
			&rec.IngestedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMediaByID(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM media_files WHERE id = ?`, id)
	return err
}

func (s *Store) ListMapPoints(ctx context.Context, limit int) ([]MapPoint, error) {
	return s.ListMapPointsFiltered(ctx, limit, MediaFilter{})
}

func (s *Store) ListMapPointsFiltered(ctx context.Context, limit int, filter MediaFilter) ([]MapPoint, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}

	where, args := buildLocationWhere(filter)
	query := fmt.Sprintf(`
		SELECT id, gps_lat, gps_lon, capture_time, file_name, kind
		FROM media_files
		WHERE gps_lat IS NOT NULL AND gps_lon IS NOT NULL
		  AND %s
		ORDER BY capture_time DESC
		LIMIT ?
	`, where)

	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]MapPoint, 0)
	for rows.Next() {
		var p MapPoint
		if err := rows.Scan(&p.ID, &p.Lat, &p.Lon, &p.CaptureTime, &p.FileName, &p.Kind); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

func (s *Store) ListLocationGroups(ctx context.Context, level string, filter MediaFilter, limit int) ([]LocationGroup, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	var col string
	switch level {
	case "state":
		col = "loc_state"
	case "county":
		col = "loc_county"
	case "city":
		col = "loc_city"
	case "road":
		col = "loc_road"
	default:
		return nil, fmt.Errorf("invalid level")
	}

	where, args := buildLocationWhere(filter)
	query := fmt.Sprintf(`
		SELECT COALESCE(NULLIF(TRIM(%s), ''), 'Unknown') AS name,
		       COUNT(1) AS count,
		       MIN(gps_lat) AS min_lat, MIN(gps_lon) AS min_lon,
		       MAX(gps_lat) AS max_lat, MAX(gps_lon) AS max_lon
		FROM media_files
		WHERE %s
		GROUP BY name
		ORDER BY count DESC, name ASC
		LIMIT ?
	`, col, where)

	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]LocationGroup, 0)
	for rows.Next() {
		var g LocationGroup
		if err := rows.Scan(&g.Name, &g.Count, &g.MinLat, &g.MinLon, &g.MaxLat, &g.MaxLon); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) ListGeoTodos(ctx context.Context, limit int) ([]GeoTodo, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, gps_lat, gps_lon
		FROM media_files
		WHERE gps_lat IS NOT NULL AND gps_lon IS NOT NULL
		  AND (loc_state IS NULL AND loc_county IS NULL AND loc_city IS NULL AND loc_display_name IS NULL)
		ORDER BY capture_time DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GeoTodo, 0)
	for rows.Next() {
		var t GeoTodo
		if err := rows.Scan(&t.ID, &t.Lat, &t.Lon); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMediaLocation(ctx context.Context, id int64, rec *MediaRecord) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE media_files SET
			loc_provider = ?,
			loc_country = ?,
			loc_state = ?,
			loc_county = ?,
			loc_city = ?,
			loc_road = ?,
			loc_house_number = ?,
			loc_postcode = ?,
			loc_display_name = ?
		WHERE id = ?
	`,
		nullStringToAny(rec.LocProvider),
		nullStringToAny(rec.Country),
		nullStringToAny(rec.State),
		nullStringToAny(rec.County),
		nullStringToAny(rec.City),
		nullStringToAny(rec.Road),
		nullStringToAny(rec.HouseNumber),
		nullStringToAny(rec.Postcode),
		nullStringToAny(rec.DisplayName),
		id,
	)
	return err
}

func (s *Store) InsertAudit(ctx context.Context, ts, actor, action string, details any, prevHash, entryHash string) error {
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO audit_logs (ts, actor, action, details_json, prev_hash, entry_hash) VALUES (?, ?, ?, ?, ?, ?)`,
		ts, actor, action, string(detailsJSON), prevHash, entryHash,
	)
	return err
}

func (s *Store) LastAuditHash(ctx context.Context) (string, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT entry_hash FROM audit_logs ORDER BY id DESC LIMIT 1`)
	var hash string
	if err := row.Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return hash, nil
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditRecord, error) {
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, ts, actor, action, details_json, entry_hash
		FROM audit_logs
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AuditRecord, 0)
	for rows.Next() {
		var rec AuditRecord
		if err := rows.Scan(&rec.ID, &rec.TS, &rec.Actor, &rec.Action, &rec.Details, &rec.Hash); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) GetGeocodeCache(ctx context.Context, provider, geocodeKey string) (*GeocodeCacheEntry, bool, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT provider, geocode_key, country, state, county, city, road, house_number, postcode, display_name, raw_json, updated_at
		FROM geocode_cache
		WHERE provider = ? AND geocode_key = ?
	`, provider, geocodeKey)

	var (
		entry                        GeocodeCacheEntry
		country, state, county, city sql.NullString
		road, houseNumber, postcode  sql.NullString
		displayName                  sql.NullString
	)

	if err := row.Scan(
		&entry.Provider,
		&entry.GeocodeKey,
		&country,
		&state,
		&county,
		&city,
		&road,
		&houseNumber,
		&postcode,
		&displayName,
		&entry.RawJSON,
		&entry.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}

	entry.Country = country.String
	entry.State = state.String
	entry.County = county.String
	entry.City = city.String
	entry.Road = road.String
	entry.HouseNumber = houseNumber.String
	entry.Postcode = postcode.String
	entry.DisplayName = displayName.String

	return &entry, true, nil
}

func (s *Store) UpsertGeocodeCache(ctx context.Context, entry *GeocodeCacheEntry) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO geocode_cache (
			provider, geocode_key, country, state, county, city, road, house_number, postcode, display_name, raw_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, geocode_key) DO UPDATE SET
			country = excluded.country,
			state = excluded.state,
			county = excluded.county,
			city = excluded.city,
			road = excluded.road,
			house_number = excluded.house_number,
			postcode = excluded.postcode,
			display_name = excluded.display_name,
			raw_json = excluded.raw_json,
			updated_at = excluded.updated_at
	`, entry.Provider, entry.GeocodeKey,
		nullable(entry.Country),
		nullable(entry.State),
		nullable(entry.County),
		nullable(entry.City),
		nullable(entry.Road),
		nullable(entry.HouseNumber),
		nullable(entry.Postcode),
		nullable(entry.DisplayName),
		entry.RawJSON,
		now,
	)
	return err
}

func nullFloatToAny(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}

func nullStringToAny(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func buildLocationWhere(filter MediaFilter) (string, []any) {
	clauses := []string{"1=1"}
	args := make([]any, 0)

	apply := func(col, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if strings.EqualFold(value, "unknown") {
			clauses = append(clauses, fmt.Sprintf("(%s IS NULL OR TRIM(%s) = '')", col, col))
			return
		}
		clauses = append(clauses, fmt.Sprintf("%s = ?", col))
		args = append(args, value)
	}

	apply("loc_state", filter.State)
	apply("loc_county", filter.County)
	apply("loc_city", filter.City)
	apply("loc_road", filter.Road)

	return strings.Join(clauses, " AND "), args
}
