package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type summaryWriter struct {
	buf     []byte
	ID      string
	NewData int
}

func (w *summaryWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		pos := bytes.IndexByte(w.buf, '\n')
		if pos < 0 {
			break
		}
		var v struct {
			Type    string `json:"message_type"`
			ID      string `json:"snapshot_id"`
			NewData int    `json:"data_blobs"`
		}
		if err := json.Unmarshal(w.buf[:pos], &v); err != nil {
			return 0, fmt.Errorf("invalid backup output: %w", err)
		}
		if v.Type == "summary" {
			w.ID = v.ID
			w.NewData = v.NewData
		}
		w.buf = w.buf[pos+1:]
	}
	if len(w.buf) > 1024*1024 {
		return 0, errors.New("oversized backend progress record")
	}
	return len(p), nil
}

// CheckpointID requires a successful backup and an explicit summary snapshot ID.
func (r Repository) CheckpointID(ctx context.Context, o BackupOptions) (string, error) {
	w := &summaryWriter{}
	r.JSON = true
	r.Quiet = false
	r.Out = io.Writer(w)
	o.Checkpoint = true
	o.DryRun = false
	if err := r.Backup(ctx, o); err != nil {
		return "", err
	}
	if !snapshotID.MatchString(w.ID) {
		return "", errors.New("backup did not confirm a complete snapshot")
	}
	s, err := r.Resolve(ctx, w.ID)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}
