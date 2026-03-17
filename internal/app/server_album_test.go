package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"businessplan/usbvault/internal/db"
)

func TestMaterializeAlbumFolderCreatesUniqueSymlinksAndPrunesStaleEntries(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("USBVAULT_DATA_DIR", filepath.Join(rootDir, "data"))

	store, err := db.Open(filepath.Join(rootDir, "usbvault.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	originalA := filepath.Join(rootDir, "library", "a", "DJI_0001.MP4")
	originalB := filepath.Join(rootDir, "library", "b", "DJI_0001.MP4")
	if err := os.MkdirAll(filepath.Dir(originalA), 0o750); err != nil {
		t.Fatalf("MkdirAll originalA: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(originalB), 0o750); err != nil {
		t.Fatalf("MkdirAll originalB: %v", err)
	}
	if err := os.WriteFile(originalA, []byte("a"), 0o640); err != nil {
		t.Fatalf("WriteFile originalA: %v", err)
	}
	if err := os.WriteFile(originalB, []byte("b"), 0o640); err != nil {
		t.Fatalf("WriteFile originalB: %v", err)
	}

	ctx := context.Background()
	ts := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for idx, destPath := range []string{originalA, originalB} {
		rec := &db.MediaRecord{
			Kind:        "video",
			FileName:    "DJI_0001.MP4",
			Extension:   ".mp4",
			SourceMount: "/Volumes/Test",
			SourcePath:  fmt.Sprintf("/DCIM/%d/DJI_0001.MP4", idx),
			DestPath:    destPath,
			SizeBytes:   1000 + int64(idx),
			CRC32:       fmt.Sprintf("%08x", idx+1),
			SHA256:      fmt.Sprintf("%064x", idx+1),
			CaptureTime: ts,
			GPSLat:      sql.NullFloat64{Float64: 39.7, Valid: true},
			GPSLon:      sql.NullFloat64{Float64: -104.9, Valid: true},
			Metadata:    "{}",
			SourceMTime: ts,
			IngestedAt:  ts,
		}
		if err := store.InsertMedia(ctx, rec); err != nil {
			t.Fatalf("InsertMedia(%d): %v", idx, err)
		}
	}

	album, err := store.CreateAlbum(ctx, "Deer on Golf Course")
	if err != nil {
		t.Fatalf("CreateAlbum: %v", err)
	}
	ids := mustMediaIDsByDestPath(t, store, []string{originalA, originalB})
	if _, _, err := store.AddMediaToAlbum(ctx, album.ID, ids); err != nil {
		t.Fatalf("AddMediaToAlbum: %v", err)
	}

	app := &App{store: store}
	albumFolder := filepath.Join(rootDir, "data", "album-folders", albumFolderDirName(album))
	if err := os.MkdirAll(albumFolder, 0o750); err != nil {
		t.Fatalf("MkdirAll album folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumFolder, "stale.txt"), []byte("stale"), 0o640); err != nil {
		t.Fatalf("WriteFile stale: %v", err)
	}

	folderPath, linked, missing, err := app.materializeAlbumFolder(ctx, album)
	if err != nil {
		t.Fatalf("materializeAlbumFolder: %v", err)
	}
	if linked != 2 || missing != 0 {
		t.Fatalf("materializeAlbumFolder linked=%d missing=%d, want 2/0", linked, missing)
	}
	if folderPath != albumFolder {
		t.Fatalf("folderPath = %q, want %q", folderPath, albumFolder)
	}

	entries, err := os.ReadDir(albumFolder)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ReadDir returned %d entries, want 2", len(entries))
	}
	if _, err := os.Stat(filepath.Join(albumFolder, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale entry should be removed, err=%v", err)
	}

	targets := make(map[string]string, len(entries))
	for _, entry := range entries {
		linkPath := filepath.Join(albumFolder, entry.Name())
		target, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("Readlink(%q): %v", entry.Name(), err)
		}
		targets[entry.Name()] = filepath.Clean(target)
	}

	if targets["DJI_0001.MP4"] == "" {
		t.Fatalf("expected DJI_0001.MP4 symlink, got %#v", targets)
	}
	if targets["DJI_0001 (2).MP4"] == "" {
		t.Fatalf("expected duplicate-safe symlink name, got %#v", targets)
	}
}

func mustMediaIDsByDestPath(t *testing.T, store *db.Store, destPaths []string) []int64 {
	t.Helper()

	out := make([]int64, 0, len(destPaths))
	for _, destPath := range destPaths {
		var id int64
		if err := store.DB.QueryRow(`SELECT id FROM media_files WHERE dest_path = ?`, destPath).Scan(&id); err != nil {
			t.Fatalf("lookup media id for %q: %v", destPath, err)
		}
		out = append(out, id)
	}
	return out
}
