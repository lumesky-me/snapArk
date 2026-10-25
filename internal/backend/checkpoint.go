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
	buf      []byte
	ID       string
	NewData  int
	Files    uint64
	Bytes    uint64
	NewID    string
	Progress io.Writer
}

func (w *summaryWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		pos := bytes.IndexByte(w.buf, '\n')
		if pos < 0 {
			break
		}
		var v struct {
			Type      string `json:"message_type"`
			ID        string `json:"snapshot_id"`
			NewData   int    `json:"data_blobs"`
			Files     uint64 `json:"total_files_processed"`
			Bytes     uint64 `json:"total_bytes_processed"`
			NewID     string `json:"new_snapshot_id"`
			FilesDone uint64 `json:"files_done"`
			BytesDone uint64 `json:"bytes_done"`
		}
		if err := json.Unmarshal(w.buf[:pos], &v); err != nil {
			return 0, fmt.Errorf("invalid backup output: %w", err)
		}
		if v.Type == "changed" {
			w.NewID = v.NewID
		}
		if v.Type == "status" && w.Progress != nil {
			fmt.Fprintf(w.Progress, "backup: %d files, %d bytes processed\n", v.FilesDone, v.BytesDone)
		}
		if v.Type == "summary" {
			w.ID = v.ID
			w.NewData = v.NewData
			w.Files = v.Files
			w.Bytes = v.Bytes
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
	o.Checkpoint = true
	return r.BackupID(ctx, o, false)
}

func (r Repository) BackupID(ctx context.Context, o BackupOptions, progress bool) (string, error) {
	w := &summaryWriter{}
	out := r.Out
	if progress && !r.Quiet && !r.JSON {
		w.Progress = r.Err
	}
	echoJSON := progress && r.JSON && !r.Quiet && out != nil
	r.JSON = true
	r.Quiet = false
	r.Out = io.Writer(w)
	if echoJSON {
		r.Out = io.MultiWriter(w, out)
	}
	o.DryRun = false
	if err := r.Backup(ctx, o); err != nil {
		return "", err
	}
	if !snapshotID.MatchString(w.ID) {
		return "", errors.New("backup did not confirm a complete snapshot")
	}
	r.SnapshotTag = ""
	r.SnapshotHost = ""
	s, err := r.Resolve(ctx, w.ID)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

// VerifiedBackup publishes the completion tag only after backup and verification
// succeed. Partial restic snapshots retain a pending tag for manual recovery.
func (r Repository) VerifiedBackup(ctx context.Context, o BackupOptions, complete string, full bool) (string, error) {
	pending := complete + ":pending"
	if complete == "" {
		return "", errors.New("completion scope is required")
	}
	tags := []string{}
	for _, tag := range o.Tags {
		if tag != complete && tag != pending {
			tags = append(tags, tag)
		}
	}
	o.Tags = append(tags, pending)
	id, e := r.BackupID(ctx, o, true)
	if e != nil {
		return "", e
	}
	checker := r
	checker.Out = r.Err
	checker.JSON = false
	if e = checker.Check(ctx, full); e != nil {
		return "", fmt.Errorf("snapshot %s remains pending because verification failed: %w", id, e)
	}
	writer := &summaryWriter{}
	r.JSON = true
	r.Quiet = true
	r.Out = writer
	if e = r.Execute(ctx, "tag", "--remove", pending, "--add", complete, id); e != nil {
		return "", e
	}
	if !snapshotID.MatchString(writer.NewID) {
		return "", errors.New("backup data was verified but completion tagging was not confirmed; inspect pending snapshots")
	}
	return writer.NewID, nil
}
