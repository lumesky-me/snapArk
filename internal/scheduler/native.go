package scheduler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command receives a program and literal argv, never a shell command line.
type Command func(program string, args []string, input []byte) ([]byte, error)
type State struct {
	State     string `json:"state"`
	Installed bool   `json:"installed"`
	Enabled   *bool  `json:"enabled,omitempty"`
	Loaded    *bool  `json:"loaded,omitempty"`
	Detail    string `json:"detail,omitempty"`
}
type receipt struct {
	Version    int    `json:"version"`
	Owner      string `json:"owner"`
	Definition string `json:"definition"`
}
type receipts struct{ Dir, Name, Owner string }

func (r receipts) path() string { return filepath.Join(r.Dir, r.Name+".json") }
func (r receipts) read() (receipt, error) {
	var v receipt
	info, e := os.Lstat(r.path())
	if e != nil {
		return v, e
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return v, errors.New("invalid scheduler ownership record")
	}
	b, e := os.ReadFile(r.path())
	if e != nil {
		return v, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&v); e != nil {
		return v, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return v, errors.New("trailing scheduler ownership data")
	}
	if v.Version != 1 || v.Owner != r.Owner || v.Definition == "" {
		return v, errors.New("scheduler ownership does not match this profile and configuration")
	}
	return v, nil
}
func (r receipts) save(definition string) error {
	f, e := os.CreateTemp(r.Dir, ".schedule-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(receipt{1, r.Owner, definition})
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), r.path())
}
func (r receipts) lock() (func(), error) {
	if e := os.MkdirAll(r.Dir, 0700); e != nil {
		return nil, e
	}
	p := r.path() + ".lock"
	if e := os.Mkdir(p, 0700); e != nil {
		return nil, fmt.Errorf("scheduler busy or interrupted; inspect %s without stealing its lock: %w", p, e)
	}
	return func() { _ = os.Remove(p) }, nil
}
func fingerprint(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Task Scheduler canonicalizes XML at registration. Compare its own exported
// definition, ignoring only Settings/Enabled, which is a supported lifecycle change.
func taskFingerprint(data string) (string, error) {
	d := xml.NewDecoder(strings.NewReader(data))
	// COM exports a Unicode string with a UTF-16 declaration. JSON has already
	// converted that string to UTF-8; the declaration no longer describes bytes.
	d.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "utf-16") {
			return input, nil
		}
		return nil, fmt.Errorf("unsupported task XML encoding %q", label)
	}
	var b bytes.Buffer
	var stack []string
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
		switch v := t.(type) {
		case xml.StartElement:
			stack = append(stack, v.Name.Local)
			if len(stack) == 3 && stack[1] == "Settings" && stack[2] == "Enabled" {
				continue
			}
			fmt.Fprintf(&b, "<%s:%s", v.Name.Space, v.Name.Local)
			sort.Slice(v.Attr, func(i, j int) bool {
				return v.Attr[i].Name.Space+v.Attr[i].Name.Local < v.Attr[j].Name.Space+v.Attr[j].Name.Local
			})
			for _, a := range v.Attr {
				fmt.Fprintf(&b, " %s:%s=%q", a.Name.Space, a.Name.Local, a.Value)
			}
			b.WriteByte('>')
		case xml.EndElement:
			ignored := len(stack) == 3 && stack[1] == "Settings" && stack[2] == "Enabled"
			if !ignored {
				fmt.Fprintf(&b, "</%s:%s>", v.Name.Space, v.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 3 && stack[1] == "Settings" && stack[2] == "Enabled" {
				continue
			}
			if len(bytes.TrimSpace(v)) > 0 {
				fmt.Fprintf(&b, "%q", []byte(v))
			}
		}
	}
	return fingerprint(b.Bytes()), nil
}
