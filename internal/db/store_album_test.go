package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestListAlbumMediaLinks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)

	recA := &MediaRecord{
		Kind:        "image",
		FileName:    "B.JPG",
		Extension:   ".jpg",
		SourceMount: "/Volumes/Test",
		SourcePath:  "/DCIM/B.JPG",
		DestPath:    "/tmp/usbvault/B.JPG",
		SizeBytes:   1001,
		CRC32:       "00000001",
		SHA256:      fmt.Sprintf("%064x", 1),
		CaptureTime: ts,
		GPSLat:      sql.NullFloat64{Float64: 39.7, Valid: true},
		GPSLon:      sql.NullFloat64{Float64: -104.9, Valid: true},
		Metadata:    "{}",
		SourceMTime: ts,
		IngestedAt:  ts,
	}
	recB := &MediaRecord{
		Kind:        "video",
		FileName:    "A.MP4",
		Extension:   ".mp4",
		SourceMount: "/Volumes/Test",
		SourcePath:  "/DCIM/A.MP4",
		DestPath:    "/tmp/usbvault/A.MP4",
		SizeBytes:   1002,
		CRC32:       "00000002",
		SHA256:      fmt.Sprintf("%064x", 2),
		CaptureTime: ts,
		GPSLat:      sql.NullFloat64{Float64: 39.7, Valid: true},
		GPSLon:      sql.NullFloat64{Float64: -104.9, Valid: true},
		Metadata:    "{}",
		SourceMTime: ts,
		IngestedAt:  ts,
	}

	for _, rec := range []*MediaRecord{recA, recB} {
		if err := store.InsertMedia(ctx, rec); err != nil {
			t.Fatalf("InsertMedia(%s): %v", rec.FileName, err)
		}
	}

	album, err := store.CreateAlbum(ctx, "Test Album")
	if err != nil {
		t.Fatalf("CreateAlbum: %v", err)
	}

	ids := mustMediaIDsByDestPath(t, store, []string{recA.DestPath, recB.DestPath})
	if added, skipped, err := store.AddMediaToAlbum(ctx, album.ID, ids); err != nil {
		t.Fatalf("AddMediaToAlbum: %v", err)
	} else if added != 2 || skipped != 0 {
		t.Fatalf("AddMediaToAlbum added=%d skipped=%d, want 2/0", added, skipped)
	}

	links, err := store.ListAlbumMediaLinks(ctx, album.ID)
	if err != nil {
		t.Fatalf("ListAlbumMediaLinks: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("ListAlbumMediaLinks returned %d rows, want 2", len(links))
	}
	if links[0].FileName != "A.MP4" || links[1].FileName != "B.JPG" {
		t.Fatalf("ListAlbumMediaLinks order = %#v", links)
	}
}

func mustMediaIDsByDestPath(t *testing.T, store *Store, destPaths []string) []int64 {
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
