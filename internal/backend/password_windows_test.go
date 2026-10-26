package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func credentialACL(t *testing.T, file, dacl string) {
	t.Helper()
	sd, e := windows.SecurityDescriptorFromString(dacl)
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, user.User.Sid, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
}
func TestPasswordACLNative(t *testing.T) {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sid := user.User.Sid.String()
	for _, tc := range []struct {
		name, acl string
		ok        bool
	}{
		{"private", "D:P(A;;FA;;;" + sid + ")", true},
		{"system", "D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"inherited-private", "D:P(A;ID;FA;;;" + sid + ")", true},
		{"everyone-readable", "D:P(A;;FA;;;" + sid + ")(A;;FR;;;WD)", false},
		{"inherited-users", "D:P(A;;FA;;;" + sid + ")(A;ID;FR;;;BU)", false},
		{"null", "D:NO_ACCESS_CONTROL", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "credential ü.txt")
			if e := os.WriteFile(p, []byte("disposable-test-password\n"), 0600); e != nil {
				t.Fatal(e)
			}
			credentialACL(t, p, tc.acl)
			release, e := ProtectPassword(p)
			if (e == nil) != tc.ok {
				t.Fatalf("accepted=%v want=%v: %v", e == nil, tc.ok, e)
			}
			if e == nil {
				if e := os.WriteFile(p, []byte("replaced"), 0600); e == nil {
					t.Error("protected password was writable")
				}
				if e := os.Rename(p, p+".moved"); e == nil {
					t.Error("protected password was replaceable")
				}
				if e := os.Rename(filepath.Dir(p), filepath.Dir(p)+".moved"); e == nil {
					t.Error("protected parent could be renamed")
				}
				release()
			}
		})
	}
	p := filepath.Join(t.TempDir(), "missing")
	if _, e = ProtectPassword(p); e == nil {
		t.Fatal("missing file accepted")
	}
	if _, e = ProtectPassword(filepath.Dir(p)); e == nil {
		t.Fatal("directory accepted")
	}
	if _, e = ProtectPassword(`\\server\share\password`); e == nil {
		t.Fatal("UNC accepted")
	}
	if _, e = ProtectPassword(p + ":stream"); e == nil {
		t.Fatal("alternate data stream accepted")
	}
}
func TestPasswordRejectsLinksNative(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "password")
	if e := os.WriteFile(p, []byte("test"), 0600); e != nil {
		t.Fatal(e)
	}
	user, _ := windows.GetCurrentProcessToken().GetTokenUser()
	credentialACL(t, p, "D:P(A;;FA;;;"+user.User.Sid.String()+")")
	link := filepath.Join(dir, "link")
	if e := os.Symlink(p, link); e != nil {
		t.Skipf("symlinks unavailable: %v", e)
	}
	if _, e := ProtectPassword(link); e == nil || !strings.Contains(e.Error(), "reparse") {
		t.Fatalf("symlink accepted: %v", e)
	}
}

func TestCredentialParentJunctionRefused(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if e := os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	password := filepath.Join(private, "password")
	if e := os.WriteFile(password, []byte("disposable"), 0600); e != nil {
		t.Fatal(e)
	}
	user, _ := windows.GetCurrentProcessToken().GetTokenUser()
	credentialACL(t, password, "D:P(A;;FA;;;"+user.User.Sid.String()+")")
	alias := filepath.Join(root, "alias")
	if out, e := exec.Command("cmd", "/c", "mklink", "/J", alias, private).CombinedOutput(); e != nil {
		t.Fatalf("%s %v", out, e)
	}
	defer os.Remove(alias)
	if _, e := ProtectPassword(filepath.Join(alias, "password")); e == nil || !strings.Contains(e.Error(), "reparse") {
		t.Fatal("credential parent junction accepted", e)
	}
}
