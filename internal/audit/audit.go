package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"businessplan/usbvault/internal/db"
)

type Logger struct {
	store *db.Store
}

func New(store *db.Store) *Logger {
	return &Logger{store: store}
}

func (l *Logger) Log(ctx context.Context, actor, action string, details map[string]any) error {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	prev, err := l.store.LastAuditHash(ctx)
	if err != nil {
		return err
	}

	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}

	raw := fmt.Sprintf("%s|%s|%s|%s|%s", ts, actor, action, string(detailsJSON), prev)
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])

	return l.store.InsertAudit(ctx, ts, actor, action, details, prev, hash)
}
