package backend

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsUnreadableBackupHasNoCompletion(t *testing.T) {
	r, src, ctx := fixture(t)
	mustWrite(t, filepath.Join(src, "safe"), []byte("previous good backup"))
	complete := "veyra:test-windows:complete"
	previous, e := r.VerifiedBackup(ctx, BackupOptions{Sources: []string{src}}, complete, false)
	if e != nil {
		t.Fatal(e)
	}
	denied := filepath.Join(src, "denied")
	mustWrite(t, denied, []byte("must not certify its omission"))
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sid := user.User.Sid.String()
	credentialACL(t, denied, "D:P(D;;FR;;;"+sid+")(A;;FA;;;"+sid+")")
	defer credentialACL(t, denied, "D:P(A;;FA;;;"+sid+")")
	if f, e := os.Open(denied); e == nil {
		f.Close()
		t.Fatal("fixture did not deny file reads")
	}
	id, e := r.VerifiedBackup(ctx, BackupOptions{Sources: []string{src}}, complete, false)
	if e == nil || id != "" {
		t.Fatal("unreadable Windows source was certified complete", id, e)
	}
	snapshots, e := r.Snapshots(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range snapshots {
		for _, tag := range s.Tags {
			if tag == complete && s.ID != previous {
				t.Fatal("failed backup published a completion tag")
			}
		}
	}
	if _, e = r.Resolve(ctx, previous); e != nil {
		t.Fatal("failed backup lost previous snapshot", e)
	}
}
