package cli

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	argv := []string{exe, "backup", "--scheduled", "--config", store.Path, "--profile", p.Name, "--password-file", password}
	for _, arg := range argv {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return nil, errors.New("scheduler paths must not contain line breaks")
		}
	}
	linux := []string{}
	win := []string{}
	mac := ""
	for i, arg := range argv {
		linux = append(linux, systemdArg(arg))
		mac += "<string>" + xmlText(arg) + "</string>"
		if i > 0 {
			win = append(win, windowsArg(arg))
		}
	}
	name := "veyra-" + p.Name
	result := map[string]string{
		"notice":          "PREVIEW ONLY. Nothing installed. Protect the password file with chmod 600 or a restrictive Windows ACL. A missing drive causes failure, not deletion; no retention runs. Linux retries at timer activation after boot; macOS RunAtLoad and Windows StartWhenAvailable request catch-up. No guarantee is made while the user session or scheduler is unavailable.",
		name + ".service": "[Unit]\nDescription=VEYRA backup " + p.Name + "\n[Service]\nType=oneshot\nExecStart=" + strings.Join(linux, " ") + "\n",
		name + ".timer":   fmt.Sprintf("[Unit]\nDescription=VEYRA timer %s\n[Timer]\nOnBootSec=5m\nOnUnitActiveSec=%dh\n[Install]\nWantedBy=timers.target\n", p.Name, hours),
		name + ".plist":   fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>me.lumesky.veyra.%s</string><key>ProgramArguments</key><array>%s</array><key>StartInterval</key><integer>%d</integer><key>RunAtLoad</key><true/></dict></plist>`, p.Name, mac, hours*3600),
		name + ".xml":     fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><TimeTrigger><Repetition><Interval>PT%dH</Interval><StopAtDurationEnd>false</StopAtDurationEnd></Repetition><StartBoundary>%s</StartBoundary><Enabled>true</Enabled></TimeTrigger></Triggers><Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="Author"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions></Task>`, hours, time.Now().Format("2006-01-02")+"T00:00:00", xmlText(exe), xmlText(strings.Join(win, " "))),
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
