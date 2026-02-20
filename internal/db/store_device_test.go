package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestListDeviceGroups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	ts := time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)

	inserts := []*MediaRecord{
		{
			Kind:        "image",
			FileName:    "DJI_0001.JPG",
			Extension:   ".jpg",
			SourceMount: "/Volumes/Test",
			SourcePath:  "/DCIM/DJI_0001.JPG",
			DestPath:    "/tmp/usbvault/dji_0001.jpg",
			SizeBytes:   1001,
			CRC32:       "00000001",
			SHA256:      "0000000000000000000000000000000000000000000000000000000000000001",
			CaptureTime: ts,
			GPSLat:      sql.NullFloat64{Float64: 39.7, Valid: true},
			GPSLon:      sql.NullFloat64{Float64: -104.9, Valid: true},
			Make:        sql.NullString{String: "DJI", Valid: true},
			Model:       sql.NullString{String: "Mini 4 Pro", Valid: true},
			Metadata:    "{}",
			SourceMTime: ts,
			IngestedAt:  ts,
		},
		{
			Kind:        "video",
			FileName:    "DJI_0002.MP4",
			Extension:   ".mp4",
			SourceMount: "/Volumes/Test",
			SourcePath:  "/DCIM/DJI_0002.MP4",
			DestPath:    "/tmp/usbvault/dji_0002.mp4",
			SizeBytes:   1002,
			CRC32:       "00000002",
			SHA256:      "0000000000000000000000000000000000000000000000000000000000000002",
			CaptureTime: ts,
			GPSLat:      sql.NullFloat64{Float64: 39.7, Valid: true},
			GPSLon:      sql.NullFloat64{Float64: -104.9, Valid: true},
			Make:        sql.NullString{String: "DJI", Valid: true},
			Model:       sql.NullString{String: "Mini 4 Pro", Valid: true},
			Metadata:    "{}",
			SourceMTime: ts,
			IngestedAt:  ts,
		},
		{
			Kind:        "image",
			FileName:    "IMG_1000.JPG",
			Extension:   ".jpg",
			SourceMount: "/Volumes/Test",
			SourcePath:  "/DCIM/IMG_1000.JPG",
			DestPath:    "/tmp/usbvault/img_1000.jpg",
			SizeBytes:   1003,
			CRC32:       "00000003",
			SHA256:      "0000000000000000000000000000000000000000000000000000000000000003",
			CaptureTime: ts,
			GPSLat:      sql.NullFloat64{Float64: 39.7, Valid: true},
			GPSLon:      sql.NullFloat64{Float64: -104.9, Valid: true},
			Metadata:    "{}",
			SourceMTime: ts,
			IngestedAt:  ts,
		},
	}

	for _, rec := range inserts {
		if err := store.InsertMedia(ctx, rec); err != nil {
			t.Fatalf("InsertMedia: %v", err)
		}
	}

	groups, err := store.ListDeviceGroups(ctx, MediaFilter{}, 10)
	if err != nil {
		t.Fatalf("ListDeviceGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("ListDeviceGroups returned %d groups, want 2", len(groups))
	}

	if groups[0].Make != "DJI" || groups[0].Model != "Mini 4 Pro" || groups[0].Count != 2 {
		t.Fatalf("unexpected first group: %+v", groups[0])
	}
	if groups[1].Unset != true || groups[1].Count != 1 {
		t.Fatalf("unexpected unknown-device group: %+v", groups[1])
	}
}
