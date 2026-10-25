package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
	"github.com/lumesky-me/veyra/internal/scheduler"
)

func (a App) manageSchedule(ctx context.Context, p config.Profile, path, password, action string, confirm bool) int {
	fail := func(e error) int { fmt.Fprintln(a.Err, e); return 1 }
	if runtime.GOOS != "linux" {
		return fail(fmt.Errorf("automatic scheduler management is currently Linux user-systemd only; use schedule preview and platform instructions"))
	}
	root, e := os.UserConfigDir()
	if e != nil {
		return fail(e)
	}
	store, e := configStore(path)
	if e != nil {
		return fail(e)
	}
	digest := sha256.Sum256([]byte(store.Path))
	owner := p.ID + ":" + hex.EncodeToString(digest[:])
	m := scheduler.Manager{Dir: filepath.Join(root, "systemd", "user"), Name: "veyra-" + p.Name, Owner: owner, Run: func(args ...string) error {
		c := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
		c.Env = backend.Environment()
		c.Stdout = a.Out
		c.Stderr = a.Err
		return c.Run()
	}}
	if action == "status" {
		if e = m.Owned(); e != nil {
			return fail(e)
		}
		returnCode := 0
		if e = m.Run("show", m.Name+".timer", "--property=ActiveState", "--property=UnitFileState", "--property=NextElapseUSecMonotonic"); e != nil {
			returnCode = fail(e)
		}
		return returnCode
	}
	if !confirm {
		return fail(fmt.Errorf("%s changes the current-user scheduler; inspect schedule preview first, then repeat with --confirm", action))
	}
	switch action {
	case "install":
		if password == "" || !filepath.IsAbs(password) {
			return fail(fmt.Errorf("noninteractive installation requires --password-file"))
		}
		r := backend.Repository{Path: p.Repository, PasswordFile: password}
		if e = r.Validate(ctx); e != nil {
			return fail(e)
		}
		files, e := schedule(p, path, password)
		if e != nil {
			return fail(e)
		}
		e = m.Install(files)
		if e != nil {
			return fail(e)
		}
	case "disable":
		if e = m.Disable(); e != nil {
			return fail(e)
		}
	case "remove":
		if e = m.Remove(); e != nil {
			return fail(e)
		}
	default:
		return fail(fmt.Errorf("unknown scheduling operation; use preview, install, status, disable or remove"))
	}
	fmt.Fprintln(a.Out, "Current-user scheduler operation completed; no lingering or system-wide settings changed.")
	return 0
}
