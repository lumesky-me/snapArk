package journal

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrivateWindowsHistory(t *testing.T) {
	root := t.TempDir()
	broad := filepath.Join(root, "broad-parent")
	if e := os.Mkdir(broad, 0700); e != nil {
		t.Fatal(e)
	}
	// A permissive parent must not make newly created history/archives readable
	// to Everyone. This changes only this disposable test directory's DACL.
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(broad, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(broad, "private")
	if e = privateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	child := filepath.Join(dir, "record.json")
	if e = os.WriteFile(child, []byte("nonsecret history"), 0600); e != nil {
		t.Fatal(e)
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{dir, child} {
		actual, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if e != nil {
			t.Fatal(e)
		}
		dacl, _, e := actual.DACL()
		if e != nil || dacl == nil {
			t.Fatal(e)
		}
		for i := uint32(0); i < uint32(dacl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if e = windows.GetAce(dacl, i, &ace); e != nil {
				t.Fatal(e)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !windows.EqualSid(sid, user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
				t.Fatalf("unexpected history access principal %s", sid.String())
			}
		}
	}
	if e = privateDirectory(dir); e == nil {
		t.Fatal("existing directory was changed")
	}
}
