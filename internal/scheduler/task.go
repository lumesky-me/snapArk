package scheduler

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// PowerShell hosts the documented Task Scheduler COM API. All variable data
// travels as JSON over stdin; no path or task XML is interpolated into code.
const taskScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
try {
 $r = [Console]::In.ReadToEnd() | ConvertFrom-Json
 $s = New-Object -ComObject 'Schedule.Service'
 $s.Connect()
 $f = $s.GetFolder('\')
 $t = $null
 try { $t = $f.GetTask([string]$r.name) } catch {
  $e = $_.Exception
  while ($null -ne $e.InnerException) { $e = $e.InnerException }
  if ($e.HResult -ne -2147024894) { throw }
 }
 if ($r.action -ne 'inspect' -and $r.action -ne 'create') {
  if ($null -eq $t -or $t.Xml -cne [string]$r.expected) { throw 'Task changed or is missing; nothing modified.' }
 }
 switch ($r.action) {
  'inspect' { }
  'create' {
   if ($null -ne $t) { throw 'A task already exists; nothing overwritten.' }
   $t = $f.RegisterTask([string]$r.name,[string]$r.xml,2,$null,$null,3,$null)
  }
  'replace' {
   $d = $s.NewTask(0)
   $d.XmlText = [string]$r.xml
   $d.Settings.Enabled = [bool]$t.Enabled
   $t = $f.RegisterTaskDefinition([string]$r.name,$d,4,$null,$null,3,$null)
  }
  'enable' { $t.Enabled = $true }
  'disable' { $t.Enabled = $false }
  'remove' { $f.DeleteTask([string]$r.name,0); $t = $null }
  default { throw 'Unknown task operation.' }
 }
 if ($null -eq $t) { @{ exists=$false } | ConvertTo-Json -Compress }
 else {
  $last = ''; $next = ''
  if ($t.LastRunTime.Year -gt 1900) { $last = $t.LastRunTime.ToString('o') }
  if ($t.NextRunTime.Year -gt 1900) { $next = $t.NextRunTime.ToString('o') }
  @{ exists=$true; xml=$t.Xml; enabled=[bool]$t.Enabled; native_state=[int]$t.State; last_result=[long]$t.LastTaskResult; last_run=$last; next_run=$next } | ConvertTo-Json -Compress
 }
} catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }
`

func encodedPowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(b)
}

type taskObservation struct {
	LastResult  int64  `json:"last_result"`
	LastRun     string `json:"last_run"`
	NextRun     string `json:"next_run"`
	Exists      bool   `json:"exists"`
	XML         string `json:"xml"`
	Enabled     bool   `json:"enabled"`
	NativeState int    `json:"native_state"`
}
type TaskManager struct {
	Dir, Name, Owner string
	Command          Command
}

func (m TaskManager) records() receipts { return receipts{m.Dir, m.Name, m.Owner} }
func (m TaskManager) call(action, definition, expected string) (taskObservation, error) {
	var state taskObservation
	data, _ := json.Marshal(map[string]string{"action": action, "name": m.Name, "xml": definition, "expected": expected})
	out, e := m.Command("powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPowerShell(taskScript)}, data)
	if e != nil {
		return state, fmt.Errorf("Task Scheduler %s failed: %w", action, e)
	}
	out = bytes.TrimPrefix(out, []byte{0xef, 0xbb, 0xbf})
	e = json.Unmarshal(out, &state)
	if e != nil {
		return state, fmt.Errorf("unknown Task Scheduler response: %w", e)
	}
	return state, nil
}
func (m TaskManager) owned(o taskObservation) error {
	r, e := m.records().read()
	if e != nil {
		return e
	}
	// Receipt pins the native exported XML including principal, action and trigger.
	f, e := taskFingerprint(o.XML)
	if e != nil {
		return e
	}
	if f != r.Definition || !strings.Contains(o.XML, "VEYRA owner="+m.Owner) {
		return errors.New("task is unrelated or externally modified; inspect it in Task Scheduler")
	}
	return nil
}
func (m TaskManager) Status() (State, error) {
	o, e := m.call("inspect", "", "")
	if e != nil {
		return State{State: "inaccessible", Detail: e.Error()}, e
	}
	if !o.Exists {
		return State{State: "missing"}, nil
	}
	if e = m.owned(o); e != nil {
		return State{State: "conflicting", Installed: true, Detail: e.Error()}, e
	}
	state := "enabled"
	if !o.Enabled {
		state = "disabled"
	}
	return State{State: state, Installed: true, Enabled: &o.Enabled, LastResult: &o.LastResult, LastRun: o.LastRun, NextRun: o.NextRun, Detail: fmt.Sprintf("current-user interactive task; native state %d; result code is from Task Scheduler, not a repository integrity check; logged-out execution is unsupported", o.NativeState)}, nil
}
func (m TaskManager) Apply(action, definition string) error {
	release, e := m.records().lock()
	if e != nil {
		return e
	}
	defer release()
	o, e := m.call("inspect", "", "")
	if e != nil {
		return e
	}
	if action == "install" {
		if o.Exists {
			return errors.New("task already exists; use replace only after inspecting ownership")
		}
		if _, e := os.Lstat(m.records().path()); !errors.Is(e, os.ErrNotExist) {
			return errors.New("scheduler ownership record already exists; inspect the missing task before removing its record")
		}
		action = "create"
	} else {
		if !o.Exists {
			return errors.New("task is missing; nothing modified")
		}
		if e = m.owned(o); e != nil {
			return e
		}
	}
	if action == "create" || action == "replace" {
		if !strings.Contains(definition, "VEYRA owner="+m.Owner) {
			return errors.New("definition has no matching owner")
		}
	}
	updated, e := m.call(action, definition, o.XML)
	if e != nil {
		return e
	}
	if action == "remove" {
		return os.Remove(m.records().path())
	}
	if !updated.Exists {
		return errors.New("operation returned no task; inspect native state")
	}
	f, e := taskFingerprint(updated.XML)
	if e != nil {
		return e
	}
	if action == "create" || action == "replace" {
		if e = m.records().save(f); e != nil {
			return fmt.Errorf("task exists but its ownership record could not be saved; inspect the task manually: %w", e)
		}
	}
	return nil
}
