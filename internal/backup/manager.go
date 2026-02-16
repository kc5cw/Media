package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"businessplan/usbvault/internal/config"
	"businessplan/usbvault/internal/db"
)

const baseStorageSetting = "base_storage_dir"

var ErrBusy = errors.New("backup already running")
var ErrInvalidRequest = errors.New("invalid backup request")

type Request struct {
	Mode        string `json:"mode"`
	Destination string `json:"destination"`
	SSHPort     int    `json:"ssh_port"`
	APIMethod   string `json:"api_method"`
	APIToken    string `json:"api_token"`
}

type Status struct {
	State       string `json:"state"` // idle, running, success, error
	Mode        string `json:"mode"`
	Destination string `json:"destination"`
	StartedAt   string `json:"started_at"`
	UpdatedAt   string `json:"updated_at"`
	FinishedAt  string `json:"finished_at"`
	Files       int64  `json:"files"`
	Bytes       int64  `json:"bytes"`
	CurrentPath string `json:"current_path"`
	Message     string `json:"message"`
}

type Manager struct {
	store  *db.Store
	logger *log.Logger

	mu     sync.Mutex
	status Status
}

func NewManager(store *db.Store, logger *log.Logger) *Manager {
	return &Manager{
		store:  store,
		logger: logger,
		status: Status{State: "idle", Message: "No backup running."},
	}
}

func (m *Manager) GetStatus() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) Start(actor string, req Request) error {
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	req.Destination = strings.TrimSpace(req.Destination)
	req.APIMethod = strings.ToUpper(strings.TrimSpace(req.APIMethod))
	if req.APIMethod == "" {
		req.APIMethod = http.MethodPut
	}

	if err := validateRequest(req); err != nil {
		return err
	}

	m.mu.Lock()
	if m.status.State == "running" {
		m.mu.Unlock()
		return ErrBusy
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	m.status = Status{
		State:       "running",
		Mode:        req.Mode,
		Destination: req.Destination,
		StartedAt:   now,
		UpdatedAt:   now,
		Message:     "Backup started...",
	}
	m.mu.Unlock()

	go m.run(actor, req)
	return nil
}

func (m *Manager) run(actor string, req Request) {
	ctx := context.Background()
	baseStorage, ok, err := m.store.GetSetting(ctx, baseStorageSetting)
	if err != nil {
		m.failf("database error: %v", err)
		return
	}
	if !ok || strings.TrimSpace(baseStorage) == "" {
		m.failf("base storage is not configured")
		return
	}
	baseStorage = filepath.Clean(baseStorage)

	var runErr error
	if req.Mode == "rsync" {
		runErr = m.runRsync(baseStorage, req.Destination)
	} else {
		runErr = m.runArchiveTransfer(baseStorage, req)
	}
	if runErr != nil {
		m.failf("%v", runErr)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	m.status.State = "success"
	m.status.UpdatedAt = now
	m.status.FinishedAt = now
	m.status.Message = fmt.Sprintf("Backup completed by %s.", actor)
}

func (m *Manager) runArchiveTransfer(baseStorage string, req Request) error {
	dbFiles := discoverDBFiles()
	reader, writer := io.Pipe()
	producerErr := make(chan error, 1)
	go func() {
		producerErr <- m.writeTarGzArchive(writer, baseStorage, dbFiles)
	}()

	var transferErr error
	switch req.Mode {
	case "ssh":
		transferErr = sendViaSSH(reader, req.Destination, req.SSHPort)
	case "s3":
		transferErr = sendViaS3(reader, req.Destination)
	case "api":
		transferErr = sendViaAPI(reader, req.Destination, req.APIMethod, req.APIToken)
	default:
		transferErr = fmt.Errorf("%w: unsupported mode %q", ErrInvalidRequest, req.Mode)
	}
	if transferErr != nil {
		_ = reader.CloseWithError(transferErr)
	}

	archiveErr := <-producerErr
	if transferErr != nil {
		return transferErr
	}
	return archiveErr
}

func (m *Manager) writeTarGzArchive(w *io.PipeWriter, baseStorage string, dbFiles []string) error {
	defer w.Close()

	gz := gzip.NewWriter(w)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	root := "usbvault-backup-" + time.Now().UTC().Format("20060102-150405")
	manifest := map[string]any{
		"created_at":     time.Now().UTC().Format(time.RFC3339),
		"base_storage":   baseStorage,
		"db_files":       dbFiles,
		"archive_format": "tar.gz",
	}
	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writeTarBytes(tw, filepath.ToSlash(filepath.Join(root, "manifest.json")), manifestJSON); err != nil {
		return err
	}

	err := filepath.WalkDir(baseStorage, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(baseStorage, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && d.IsDir() {
			return filepath.SkipDir
		}

		arcName := filepath.ToSlash(filepath.Join(root, "media", rel))
		if d.IsDir() {
			return writeTarDir(tw, arcName, info)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := writeTarFile(tw, arcName, path, info); err != nil {
			return err
		}
		m.bumpProgress(path, info.Size())
		return nil
	})
	if err != nil {
		return err
	}

	for _, dbPath := range dbFiles {
		info, err := os.Stat(dbPath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		arcName := filepath.ToSlash(filepath.Join(root, "db", filepath.Base(dbPath)))
		if err := writeTarFile(tw, arcName, dbPath, info); err != nil {
			return err
		}
		m.bumpProgress(dbPath, info.Size())
	}

	return nil
}

func (m *Manager) runRsync(baseStorage, destination string) error {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return fmt.Errorf("%w: destination is required", ErrInvalidRequest)
	}
	m.setMessage("Running rsync transfer...")

	if isLocalPath(destination) {
		if err := os.MkdirAll(destination, 0o750); err != nil {
			return fmt.Errorf("create rsync destination: %w", err)
		}
	}

	mediaDest := appendDest(destination, "media")
	dbDest := appendDest(destination, "db")

	if err := ensureRemoteDirIfSSH(mediaDest); err != nil {
		return err
	}
	if err := ensureRemoteDirIfSSH(dbDest); err != nil {
		return err
	}

	mediaSrc := withTrailingSep(baseStorage)
	if err := runCommand("rsync", "-az", "--delete", mediaSrc, withTrailingSep(mediaDest)); err != nil {
		return err
	}

	for _, dbPath := range discoverDBFiles() {
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		if err := runCommand("rsync", "-az", dbPath, withTrailingSep(dbDest)); err != nil {
			return err
		}
	}
	return nil
}

func validateRequest(req Request) error {
	switch req.Mode {
	case "ssh", "s3", "api", "rsync":
	default:
		return fmt.Errorf("%w: mode must be ssh, rsync, s3, or api", ErrInvalidRequest)
	}
	if req.Destination == "" {
		return fmt.Errorf("%w: destination is required", ErrInvalidRequest)
	}
	if req.Mode == "api" {
		switch req.APIMethod {
		case http.MethodPut, http.MethodPost:
		default:
			return fmt.Errorf("%w: api_method must be PUT or POST", ErrInvalidRequest)
		}
	}
	return nil
}

func sendViaSSH(r io.Reader, destination string, port int) error {
	host, remotePath, err := splitSSHDestination(destination)
	if err != nil {
		return err
	}
	args := make([]string, 0, 6)
	if port > 0 {
		args = append(args, "-p", strconv.Itoa(port))
	}
	args = append(args, host, "sh", "-c", "cat > "+shellQuote(remotePath))
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = r
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh upload failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func sendViaS3(r io.Reader, destination string) error {
	cmd := exec.Command("aws", "s3", "cp", "-", destination)
	cmd.Stdin = r
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("s3 upload failed (requires aws cli/config): %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func sendViaAPI(r io.Reader, destination, method, token string) error {
	req, err := http.NewRequest(method, destination, r)
	if err != nil {
		return fmt.Errorf("build api request: %w", err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("api upload failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("api upload failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func splitSSHDestination(destination string) (string, string, error) {
	parts := strings.SplitN(destination, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%w: ssh destination must be user@host:/absolute/path/file.tar.gz", ErrInvalidRequest)
	}
	host := strings.TrimSpace(parts[0])
	remotePath := strings.TrimSpace(parts[1])
	if host == "" || remotePath == "" {
		return "", "", fmt.Errorf("%w: invalid ssh destination", ErrInvalidRequest)
	}
	return host, remotePath, nil
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'"
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func appendDest(dest, child string) string {
	dest = strings.TrimSpace(dest)
	child = strings.TrimPrefix(child, "/")
	if host, remote, ok := splitRemoteHostPath(dest); ok {
		remote = strings.TrimRight(remote, "/")
		if remote == "" {
			remote = "/"
		}
		if remote == "/" {
			return host + ":/" + child
		}
		return host + ":" + remote + "/" + child
	}
	return filepath.Join(dest, child)
}

func splitRemoteHostPath(dest string) (string, string, bool) {
	if strings.Contains(dest, "://") {
		return "", "", false
	}
	// Windows drive-letter path (e.g. C:\backup) is local, not host:path.
	if len(dest) >= 2 && ((dest[0] >= 'A' && dest[0] <= 'Z') || (dest[0] >= 'a' && dest[0] <= 'z')) && dest[1] == ':' {
		return "", "", false
	}
	parts := strings.SplitN(dest, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	host := strings.TrimSpace(parts[0])
	remote := strings.TrimSpace(parts[1])
	if host == "" || remote == "" {
		return "", "", false
	}
	return host, remote, true
}

func isLocalPath(dest string) bool {
	_, _, remote := splitRemoteHostPath(dest)
	return !remote
}

func withTrailingSep(path string) string {
	if _, _, remote := splitRemoteHostPath(path); remote {
		if strings.HasSuffix(path, "/") {
			return path
		}
		return path + "/"
	}
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, string(os.PathSeparator)) {
		return path
	}
	return path + string(os.PathSeparator)
}

func ensureRemoteDirIfSSH(dest string) error {
	host, remotePath, ok := splitRemoteHostPath(dest)
	if !ok {
		return nil
	}
	cmd := exec.Command("ssh", host, "mkdir", "-p", remotePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prepare remote dir failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func discoverDBFiles() []string {
	base := config.DBPath()
	out := []string{base, base + "-wal", base + "-shm"}
	filtered := make([]string, 0, len(out))
	for _, p := range out {
		if _, err := os.Stat(p); err == nil {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func writeTarDir(tw *tar.Writer, arcName string, info os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	name := filepath.ToSlash(strings.TrimSuffix(arcName, "/")) + "/"
	hdr.Name = name
	return tw.WriteHeader(hdr)
}

func writeTarFile(tw *tar.Writer, arcName, path string, info os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(arcName)
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func writeTarBytes(tw *tar.Writer, arcName string, body []byte) error {
	hdr := &tar.Header{
		Name:    filepath.ToSlash(arcName),
		Mode:    0o640,
		Size:    int64(len(body)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

func (m *Manager) bumpProgress(path string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Files++
	m.status.Bytes += size
	m.status.CurrentPath = path
	m.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

func (m *Manager) setMessage(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Message = message
	m.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

func (m *Manager) failf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	m.logger.Printf("backup failed: %s", msg)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	m.status.State = "error"
	m.status.UpdatedAt = now
	m.status.FinishedAt = now
	m.status.Message = msg
}
