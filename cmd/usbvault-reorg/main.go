package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"businessplan/usbvault/internal/config"
	"businessplan/usbvault/internal/db"
)

type mediaRow struct {
	ID          int64
	DestPath    string
	Capture     string
	State       sql.NullString
	County      sql.NullString
	City        sql.NullString
	Road        sql.NullString
	SizeBytes   int64
	SHA256      string
	SourceMTime string
}

func main() {
	var (
		apply = flag.Bool("apply", false, "apply changes (default is dry-run)")
		limit = flag.Int("limit", 0, "limit number of files to process (0 = all)")
	)
	flag.Parse()

	logger := log.New(os.Stdout, "[usbvault-reorg] ", log.LstdFlags|log.Lmicroseconds)
	ctx := context.Background()

	store, err := db.Open(config.DBPath())
	if err != nil {
		logger.Fatalf("open db: %v", err)
	}
	defer store.Close()

	base, ok, err := store.GetSetting(ctx, "base_storage_dir")
	if err != nil {
		logger.Fatalf("read base_storage_dir: %v", err)
	}
	if !ok || strings.TrimSpace(base) == "" {
		logger.Fatalf("base_storage_dir not configured")
	}
	base = filepath.Clean(base)

	logger.Printf("base storage: %s", base)
	if *apply {
		logger.Printf("mode: APPLY")
	} else {
		logger.Printf("mode: DRY-RUN (use --apply to perform moves)")
	}

	rows, err := listMediaRows(ctx, store.DB, *limit)
	if err != nil {
		logger.Fatalf("list media: %v", err)
	}

	var (
		total       = len(rows)
		toMove      = 0
		moved       = 0
		skipped     = 0
		missing     = 0
		errorsCount = 0
		start       = time.Now()
	)

	for _, r := range rows {
		oldPath := filepath.Clean(r.DestPath)
		if !strings.HasPrefix(oldPath, base+string(filepath.Separator)) {
			skipped++
			continue
		}
		if _, err := os.Stat(oldPath); err != nil {
			missing++
			continue
		}

		newPath, err := computeNewPath(base, r)
		if err != nil {
			errorsCount++
			logger.Printf("compute new path failed id=%d: %v", r.ID, err)
			continue
		}
		if oldPath == newPath {
			skipped++
			continue
		}
		toMove++

		if !*apply {
			logger.Printf("DRY-RUN move: %s -> %s", oldPath, newPath)
			continue
		}

		finalPath, err := allocateUniquePath(ctx, store.DB, newPath, r.ID)
		if err != nil {
			errorsCount++
			logger.Printf("allocate failed id=%d: %v", r.ID, err)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
			errorsCount++
			logger.Printf("mkdir failed id=%d: %v", r.ID, err)
			continue
		}

		if err := moveFileWithRollback(oldPath, finalPath); err != nil {
			errorsCount++
			logger.Printf("move failed id=%d: %v", r.ID, err)
			continue
		}

		if err := updateDestPath(ctx, store.DB, r.ID, finalPath); err != nil {
			// rollback best-effort
			_ = moveFileWithRollback(finalPath, oldPath)
			errorsCount++
			logger.Printf("db update failed id=%d: %v", r.ID, err)
			continue
		}

		moved++
		if moved%20 == 0 {
			logger.Printf("progress: moved %d/%d", moved, toMove)
		}
	}

	elapsed := time.Since(start)
	logger.Printf("done: total=%d to_move=%d moved=%d skipped=%d missing=%d errors=%d elapsed=%s", total, toMove, moved, skipped, missing, errorsCount, elapsed)
}

func listMediaRows(ctx context.Context, dbConn *sql.DB, limit int) ([]mediaRow, error) {
	q := `
		SELECT id, dest_path, capture_time, loc_state, loc_county, loc_city, loc_road, size_bytes, sha256, source_mtime
		FROM media_files
		ORDER BY id ASC
	`
	if limit > 0 {
		q += " LIMIT ?"
		rows, err := dbConn.QueryContext(ctx, q, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanRows(rows)
	}

	rows, err := dbConn.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func scanRows(rows *sql.Rows) ([]mediaRow, error) {
	out := make([]mediaRow, 0)
	for rows.Next() {
		var r mediaRow
		if err := rows.Scan(&r.ID, &r.DestPath, &r.Capture, &r.State, &r.County, &r.City, &r.Road, &r.SizeBytes, &r.SHA256, &r.SourceMTime); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func computeNewPath(base string, r mediaRow) (string, error) {
	tm, err := time.Parse(time.RFC3339, r.Capture)
	if err != nil {
		// fallback: keep under Unknown
		tm = time.Now().UTC()
	}

	parts := buildLocationParts(r)
	if len(parts) == 0 {
		parts = []string{"Unknown"}
	}

	folderParts := append(parts, tm.Format("2006"), tm.Format("01"), tm.Format("02"))
	folder := filepath.Join(append([]string{base}, folderParts...)...)
	filename := filepath.Base(filepath.Clean(r.DestPath))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = fmt.Sprintf("media_%d", r.ID)
	}
	return filepath.Join(folder, filename), nil
}

func buildLocationParts(r mediaRow) []string {
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
	add(r.State)
	add(r.County)
	add(r.City)
	add(r.Road)
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

func allocateUniquePath(ctx context.Context, dbConn *sql.DB, desired string, id int64) (string, error) {
	candidate := desired
	if ok, err := pathAvailable(ctx, dbConn, candidate); err != nil {
		return "", err
	} else if ok {
		return candidate, nil
	}

	ext := filepath.Ext(candidate)
	base := strings.TrimSuffix(filepath.Base(candidate), ext)
	dir := filepath.Dir(candidate)

	for i := 1; i <= 10000; i++ {
		alt := filepath.Join(dir, fmt.Sprintf("%s_r%d_%d%s", base, id, i, ext))
		ok, err := pathAvailable(ctx, dbConn, alt)
		if err != nil {
			return "", err
		}
		if ok {
			return alt, nil
		}
	}

	return "", errors.New("unable to allocate unique destination")
}

func pathAvailable(ctx context.Context, dbConn *sql.DB, path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	row := dbConn.QueryRowContext(ctx, `SELECT 1 FROM media_files WHERE dest_path = ? LIMIT 1`, path)
	var marker int
	if err := row.Scan(&marker); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func moveFileWithRollback(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else {
		var linkErr *os.LinkError
		if errors.As(err, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV) {
			return copyThenRemove(src, dst)
		}
		return err
	}
}

func copyThenRemove(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	tmp := dst + ".part"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}

	copyErr := func() error {
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		if err := out.Sync(); err != nil {
			return err
		}
		return nil
	}()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	_ = os.Chmod(dst, 0o440)

	if err := os.Remove(src); err != nil {
		return err
	}
	return nil
}

func updateDestPath(ctx context.Context, dbConn *sql.DB, id int64, dest string) error {
	_, err := dbConn.ExecContext(ctx, `UPDATE media_files SET dest_path = ? WHERE id = ?`, dest, id)
	return err
}
