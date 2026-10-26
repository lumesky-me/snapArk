package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/lumesky-me/veyra/internal/scheduler"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"context"
	"github.com/lumesky-me/veyra/internal/backend"
	"github.com/lumesky-me/veyra/internal/config"
)

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func windowsArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
			b.WriteRune(r)
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
			b.WriteRune(r)
		}
		slashes = 0
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
func systemdArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "$", "$$")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
func schedule(p config.Profile, configPath, password string) (map[string]string, error) {
	if password == "" {
		return nil, errors.New("scheduling requires --password-file; only its path is included, never its contents")
	}
	if p.ExpectedHours > 744 {
		return nil, errors.New("schedule interval cannot exceed 31 days")
	}
	hours := p.ExpectedHours
	if hours == 0 {
		hours = 24
	}
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	store, e := configStore(configPath)
	if e != nil {
		return nil, e
	}
	password, e = filepath.Abs(password)
	if e != nil {
		return nil, e
	}
	binary := p.Backend
	if binary == "" {
		if found, _, e := backend.Backend(context.Background(), ""); e == nil {
			binary = found
		}
	}
	argv := []string{exe, "backup", "--scheduled", "--config", store.Path, "--profile", p.Name, "--password-file", password}
	if binary != "" {
		argv = append(argv, "--restic", binary)
	}
	for _, arg := range argv {
		if !utf8.ValidString(arg) || strings.ContainsAny(arg, "\x00\r\n") {
			return nil, errors.New("scheduler paths must be valid UTF-8 without line breaks")
		}
	}
	linux := []string{}
	win := []string{}

	for i, arg := range argv {
		linux = append(linux, systemdArg(arg))
		if i > 0 {
			win = append(win, windowsArg(arg))
		}
	}
	owner := scheduleOwner(p, store.Path)
	name := "veyra-" + p.Name
	plist, e := scheduler.LaunchDefinition("me.lumesky."+nativeScheduleName(p, store.Path), owner, argv, hours*3600)
	if e != nil {
		return nil, e
	}
	userID := ""
	if runtime.GOOS == "windows" {
		u, e := user.Current()
		if e != nil {
			return nil, e
		}
		userID = "<UserId>" + xmlText(u.Uid) + "</UserId>"
	}
	result := map[string]string{
		"notice":          "PREVIEW ONLY. Nothing installed. Protect the password file with chmod 600 or a restrictive Windows ACL. A missing drive causes failure, not deletion; no retention runs. Linux retries at timer activation after boot; macOS RunAtLoad and Windows StartWhenAvailable request catch-up. No guarantee is made while the user session or scheduler is unavailable.",
		name + ".service": "[Unit]\nDescription=VEYRA backup " + p.Name + "\n[Service]\nType=oneshot\nExecStart=" + strings.Join(linux, " ") + "\n",
		name + ".timer":   fmt.Sprintf("[Unit]\nDescription=VEYRA timer %s\n[Timer]\nOnBootSec=5m\nOnUnitActiveSec=%dh\n[Install]\nWantedBy=timers.target\n", p.Name, hours),
		name + ".plist":   plist,
		name + ".xml":     fmt.Sprintf(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><Source>VEYRA owner=%s</Source></RegistrationInfo><Triggers><TimeTrigger><Repetition><Interval>PT%dH</Interval><StopAtDurationEnd>false</StopAtDurationEnd></Repetition><StartBoundary>%s</StartBoundary><Enabled>true</Enabled></TimeTrigger></Triggers><Principals><Principal id="Author">%s<LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="Author"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions></Task>`, xmlText(owner), hours, time.Now().Format("2006-01-02")+"T00:00:00", userID, xmlText(exe), xmlText(strings.Join(win, " "))),
	}
	for key := range result {
		if key == "notice" {
			continue
		}
		allowed := runtime.GOOS == "linux" && (strings.HasSuffix(key, ".service") || strings.HasSuffix(key, ".timer")) || runtime.GOOS == "darwin" && strings.HasSuffix(key, ".plist") || runtime.GOOS == "windows" && strings.HasSuffix(key, ".xml")
		if !allowed {
			delete(result, key)
		}
	}
	return result, nil
}

func scheduleOwner(p config.Profile, path string) string {
	digest := sha256.Sum256([]byte(path))
	return p.ID + ":" + hex.EncodeToString(digest[:])
}

func nativeScheduleName(p config.Profile, path string) string {
	digest := sha256.Sum256([]byte(path))
	return "veyra-" + p.Name + "-" + hex.EncodeToString(digest[:6])
}
