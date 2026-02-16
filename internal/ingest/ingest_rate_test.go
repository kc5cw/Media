package ingest

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"businessplan/usbvault/internal/audit"
	"businessplan/usbvault/internal/db"
	"businessplan/usbvault/internal/geocode"
)

func TestProcessMountReportsLiveMBps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "usbvault.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	baseStorage := filepath.Join(root, "library")
	if err := os.MkdirAll(baseStorage, 0o750); err != nil {
		t.Fatalf("mkdir base storage: %v", err)
	}
	if err := store.SetSetting(ctx, baseStorageSetting, baseStorage); err != nil {
		t.Fatalf("set base storage: %v", err)
	}

	mountDir := filepath.Join(root, "mount")
	if err := os.MkdirAll(mountDir, 0o750); err != nil {
		t.Fatalf("mkdir mount: %v", err)
	}

	// Two moderately large files so hashing+copying runs long enough to sample MB/s.
	if err := createTestMediaFile(filepath.Join(mountDir, "A001.mp4"), 64, 0x4d); err != nil {
		t.Fatalf("create file A001: %v", err)
	}
	if err := createTestMediaFile(filepath.Join(mountDir, "A002.mp4"), 64, 0x8b); err != nil {
		t.Fatalf("create file A002: %v", err)
	}

	manager := NewManager(store, audit.New(store), geocode.New(store), log.New(io.Discard, "", 0))

	type resultWithErr struct {
		result Result
		err    error
	}
	done := make(chan resultWithErr, 1)
	go func() {
		res, runErr := manager.ProcessMount(ctx, mountDir, "test")
		done <- resultWithErr{result: res, err: runErr}
	}()

	maxMBps := 0.0
	timeout := time.After(60 * time.Second)
	for {
		select {
		case out := <-done:
			if out.err != nil {
				t.Fatalf("process mount: %v", out.err)
			}
			if out.result.Copied == 0 {
				t.Fatalf("expected copied files > 0, got %+v", out.result)
			}
			if maxMBps <= 0 {
				t.Fatalf("expected MB/s to become > 0 during ingest, got %.4f", maxMBps)
			}
			return
		case <-timeout:
			t.Fatalf("timed out waiting for ingest completion")
		default:
			st := manager.GetStatus()
			if st.MBps > maxMBps {
				maxMBps = st.MBps
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func createTestMediaFile(path string, mebibytes int, fill byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	chunk := make([]byte, 1024*1024)
	for i := range chunk {
		chunk[i] = fill
	}
	for i := 0; i < mebibytes; i++ {
		if _, err := f.Write(chunk); err != nil {
			return err
		}
	}
	return f.Sync()
}
