package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestListMapPointsFilteredSupportsLargeLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	const rows = 1200
	for i := 0; i < rows; i++ {
		ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		rec := &MediaRecord{
			Kind:        "image",
			FileName:    fmt.Sprintf("IMG_%04d.JPG", i),
			Extension:   ".jpg",
			SourceMount: "/Volumes/Test",
			SourcePath:  fmt.Sprintf("/DCIM/%04d.JPG", i),
			DestPath:    fmt.Sprintf("/tmp/usbvault/%04d.JPG", i),
			SizeBytes:   int64(1000 + i),
			CRC32:       fmt.Sprintf("%08x", i),
			SHA256:      fmt.Sprintf("%064x", i),
			CaptureTime: ts,
			GPSLat:      sql.NullFloat64{Float64: 39.7392 + float64(i)*0.000001, Valid: true},
			GPSLon:      sql.NullFloat64{Float64: -104.9903 - float64(i)*0.000001, Valid: true},
			Metadata:    "{}",
			SourceMTime: ts,
			IngestedAt:  ts,
		}
		if err := store.InsertMedia(ctx, rec); err != nil {
			t.Fatalf("insert media %d: %v", i, err)
		}
	}

	got, err := store.ListMapPointsFiltered(ctx, 10000, MediaFilter{})
	if err != nil {
		t.Fatalf("ListMapPointsFiltered: %v", err)
	}
	if len(got) != rows {
		t.Fatalf("ListMapPointsFiltered returned %d rows, want %d", len(got), rows)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "usbvault-test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
