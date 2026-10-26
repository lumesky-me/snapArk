package backend

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProtectPassword pins each directory component and the file until release.
// Rejecting reparse points avoids validating one file and giving restic another.
// The file cannot be opened for writing/deletion during the unattended run.
// This does not defend against the same account, administrators or SYSTEM.
func ProtectPassword(path string) (func(), error) {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return nil, errors.New("unattended credentials require an absolute local-drive path; UNC and device paths are unsupported")
	}
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || strings.Contains(path[len(volume):], ":") {
		return nil, errors.New("unsupported credential path or alternate data stream")
	}
	var handles []windows.Handle
	release := func() {
		for i := len(handles) - 1; i >= 0; i-- {
			_ = windows.CloseHandle(handles[i])
		}
	}
	fail := func(e error) (func(), error) {
		release()
		return nil, fmt.Errorf("unsafe unattended password file: %w", e)
	}
	parts := strings.Split(strings.TrimLeft(path[len(volume):], `\`), `\`)
	if e := SafeNode(Node{Type: "file", Path: "/" + strings.Join(parts, "/")}); e != nil {
		return nil, errors.New("credential paths must use normal Windows names without trailing dots/spaces, reserved devices or nonportable characters")
	}
	current := volume + `\`
	// Pin the volume root as well as every named parent directory.
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			current = filepath.Join(current, parts[i])
		}
		last := i == len(parts)-1
		access, share := uint32(windows.FILE_READ_ATTRIBUTES), uint32(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
		if last {
			access = windows.GENERIC_READ | windows.READ_CONTROL
			share = windows.FILE_SHARE_READ
		}
		name, e := windows.UTF16PtrFromString(windowsObjectName(current))
		if e != nil {
			return fail(e)
		}
		h, e := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if e != nil {
			return fail(fmt.Errorf("open protected path component: %w", e))
		}
		handles = append(handles, h)
		var info windows.ByHandleFileInformation
		if e = windows.GetFileInformationByHandle(h, &info); e != nil {
			return fail(e)
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fail(errors.New("symbolic links, junctions and other reparse points are unsupported in credential paths"))
		}
		isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
		if !last && !isDir || last && isDir {
			return fail(errors.New("credential path must end at a regular file"))
		}
		if last {
			if info.FileSizeHigh == 0 && info.FileSizeLow == 0 {
				return fail(errors.New("password file is empty"))
			}
			if e = privateCredentialACL(h); e != nil {
				return fail(e)
			}
		}
	}
	return release, nil
}

func privateCredentialACL(h windows.Handle) error {
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !windows.EqualSid(owner, user.User.Sid) {
		return errors.New("credential file must be owned by the current account")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount == 0 {
		return errors.New("missing, empty or unsupported password-file DACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e := windows.GetAce(acl, i, &ace); e != nil {
			return e
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			// Opening with GENERIC_READ already established actual reader access.
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsValid() {
				return errors.New("invalid access-control principal")
			}
			if ace.Mask != 0 && !windows.EqualSid(sid, user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
				return fmt.Errorf("access granted to another principal (%s); allow only the current account, SYSTEM and Administrators", sid.String())
			}
		default:
			return errors.New("conditional, object-specific or other unsupported credential ACE; permissions cannot be established safely")
		}
	}
	return nil
}
