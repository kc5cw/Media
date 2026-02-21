package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestLocationFilterMatchesTrimmedCaseInsensitiveValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC)

	rows := []struct {
		state  string
		county string
	}{
		{state: " Colorado ", county: "Jefferson County"},
		{state: "COLORADO", county: "Arapahoe County"},
	}

	for i, row := range rows {
		ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		rec := &MediaRecord{
			Kind:        "image",
			FileName:    fmt.Sprintf("IMG_%04d.JPG", i),
			Extension:   ".jpg",
			SourceMount: "/Volumes/Test",
			SourcePath:  fmt.Sprintf("/DCIM/%04d.JPG", i),
			DestPath:    fmt.Sprintf("/tmp/usbvault/location_%04d.JPG", i),
			SizeBytes:   int64(2000 + i),
			CRC32:       fmt.Sprintf("%08x", 100+i),
			SHA256:      fmt.Sprintf("%064x", 100+i),
			CaptureTime: ts,
			GPSLat:      sql.NullFloat64{Float64: 39.7 + float64(i)*0.0001, Valid: true},
			GPSLon:      sql.NullFloat64{Float64: -104.9 - float64(i)*0.0001, Valid: true},
			State:       sql.NullString{String: row.state, Valid: true},
			County:      sql.NullString{String: row.county, Valid: true},
			Metadata:    "{}",
			SourceMTime: ts,
			IngestedAt:  ts,
		}
		if err := store.InsertMedia(ctx, rec); err != nil {
			t.Fatalf("InsertMedia(%d): %v", i, err)
		}
	}

	groups, err := store.ListLocationGroups(ctx, "county", MediaFilter{State: "Colorado"}, 50)
	if err != nil {
		t.Fatalf("ListLocationGroups: %v", err)
	}
	if len(groups) < 2 {
		t.Fatalf("expected >=2 county groups for state Colorado, got %d", len(groups))
	}
}
