package backend

import (
	"context"
	"crypto/rand"
	"github.com/lumesky-me/veyra/internal/journal"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVerificationHistory(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "data"), []byte("recover me"))
	if e := r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	store := journal.Store{Dir: filepath.Join(filepath.Dir(src), "history")}
	r.History = &store
	r.AppVersion = "test"
	if e := r.Check(ctx, true); e != nil {
		t.Fatal(e)
	}
	records, e := store.Read()
	if e != nil || len(records) != 1 || records[0].Outcome != "passed" || records[0].Depth != "full-data" || !records[0].MetadataVerified || !records[0].DataAuthenticated {
		t.Fatal(records, e)
	}
	identity, e := r.Identity(ctx)
	if e != nil || records[0].Repository != identity {
		t.Fatal("wrong repository identity", e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if e = r.Check(canceled, true); e == nil {
		t.Fatal("canceled verification passed")
	}
	records, e = store.Read()
	if e != nil || records[len(records)-1].Outcome != "interrupted" {
		t.Fatal(records, e)
	}
	wrong := filepath.Join(filepath.Dir(src), "wrong")
	if e = os.WriteFile(wrong, []byte("wrong password"), 0600); e != nil {
		t.Fatal(e)
	}
	r.PasswordFile = wrong
	r.Err = io.Discard
	if e = r.Check(context.Background(), true); e == nil {
		t.Fatal("bad password passed")
	}
	records, e = store.Read()
	if e != nil || records[len(records)-1].Outcome != "failed" {
		t.Fatal(records, e)
	}
}

type cancelAtDataRead struct {
	cancel context.CancelFunc
	seen   atomic.Bool
}

func (w *cancelAtDataRead) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "read all data") {
		w.seen.Store(true)
		w.cancel()
	}
	return len(p), nil
}
func TestInterruptedDataVerificationHistory(t *testing.T) {
	r, src, ctx := fixture(t)
	f, e := os.Create(filepath.Join(src, "random-data"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.CopyN(f, rand.Reader, 32<<20); e != nil {
		_ = f.Close()
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	if e = r.Backup(ctx, BackupOptions{Sources: []string{src}}); e != nil {
		t.Fatal(e)
	}
	store := journal.Store{Dir: filepath.Join(filepath.Dir(src), "history")}
	r.History = &store
	interrupted, cancel := context.WithCancel(ctx)
	defer cancel()
	watch := &cancelAtDataRead{cancel: cancel}
	r.Out = watch
	if e = r.Check(interrupted, true); e == nil {
		t.Fatal("canceled deep verification reported success")
	}
	if !watch.seen.Load() {
		t.Fatal("backend never entered data-reading phase")
	}
	records, e := store.Read()
	if e != nil || len(records) != 1 || records[0].Outcome != "interrupted" {
		t.Fatal("interrupted deep check recorded incorrectly", records, e)
	}
}
