// Package install downloads only the pinned restic release, after user approval.
package install

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/lumesky-me/snapArk/internal/backend"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const Version = "0.18.1"

//go:embed restic-license.txt
var license []byte
var checksums = map[string]string{
	"darwin-amd64":  "eb8543ed92ff1ddb67762daebf09f7bea4b0c37d21edb6a910bee3d4f514015f",
	"darwin-arm64":  "193fccc8bb4567b498923bc70261e104ff22be88016f0f108b035dad372ab711",
	"linux-amd64":   "680838f19d67151adba227e1570cdd8af12c19cf1735783ed1ba928bc41f363d",
	"linux-arm64":   "87f53fddde38764095e9c058a3b31834052c37e5826d2acf34e18923c006bd45",
	"windows-amd64": "0c1a713440578cb400d2e76208feb24f1b339426b075a21f73b6b2132692515d",
}

func Artifact() (name, digest string, err error) {
	digest = checksums[runtime.GOOS+"-"+runtime.GOARCH]
	if digest == "" {
		return "", "", errors.New("no pinned restic installer for this platform")
	}
	suffix := "bz2"
	if runtime.GOOS == "windows" {
		suffix = "zip"
	}
	return fmt.Sprintf("restic_%s_%s_%s.%s", Version, runtime.GOOS, runtime.GOARCH, suffix), digest, nil
}
func decode(name string, archive []byte, digest string) ([]byte, error) {
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, errors.New("restic checksum mismatch; nothing installed")
	}
	var reader io.Reader
	if strings.HasSuffix(name, ".zip") {
		z, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if e != nil {
			return nil, e
		}
		if len(z.File) != 1 || z.File[0].Name != "restic_"+Version+"_windows_amd64.exe" {
			return nil, errors.New("unexpected restic archive members")
		}
		f, e := z.File[0].Open()
		if e != nil {
			return nil, e
		}
		defer f.Close()
		reader = f
	} else {
		reader = bzip2.NewReader(bytes.NewReader(archive))
	}
	data, e := io.ReadAll(io.LimitReader(reader, 128<<20+1))
	if e != nil {
		return nil, e
	}
	if len(data) == 0 || len(data) > 128<<20 {
		return nil, errors.New("invalid restic executable size")
	}
	return data, nil
}

// Install never overwrites an executable or changes PATH. Checksums are embedded
// in snapArk, so a replaced remote checksum manifest cannot authorize a new binary.
func Install(ctx context.Context, dir string) (string, error) {
	name, digest, e := Artifact()
	if e != nil {
		return "", e
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Scheme != "https" || !(req.URL.Hostname() == "github.com" || req.URL.Hostname() == "release-assets.githubusercontent.com" || req.URL.Hostname() == "objects.githubusercontent.com") {
			return errors.New("unexpected restic download redirect")
		}
		return nil
	}}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/restic/restic/releases/download/v"+Version+"/"+name, nil)
	if e != nil {
		return "", e
	}
	response, e := client.Do(req)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("restic download: HTTP %d", response.StatusCode)
	}
	archive, e := io.ReadAll(io.LimitReader(response.Body, 64<<20+1))
	if e != nil {
		return "", e
	}
	if len(archive) > 64<<20 {
		return "", errors.New("restic download exceeds limit")
	}
	payload, e := decode(name, archive, digest)
	if e != nil {
		return "", e
	}
	return place(ctx, dir, payload)
}
func place(ctx context.Context, dir string, payload []byte) (string, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return "", e
	}
	target := filepath.Join(dir, "restic")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if _, e = os.Lstat(target); !errors.Is(e, os.ErrNotExist) {
		return "", errors.New("installation target already exists; choose an empty directory")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(dir, ".restic-*.exe")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(payload)
	if e == nil {
		e = f.Chmod(0755)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return "", e
	}
	c := exec.CommandContext(ctx, f.Name(), "version")
	c.Env = backend.Environment()
	out, e := c.Output()
	if e != nil || !strings.HasPrefix(string(out), "restic "+Version+" ") {
		return "", errors.New("downloaded restic executable failed its version check")
	}
	notice := filepath.Join(dir, "RESTIC-"+Version+"-LICENSE")
	if info, e := os.Lstat(notice); e == nil {
		if !info.Mode().IsRegular() || info.Size() != int64(len(license)) {
			return "", errors.New("existing restic license is unrelated or not a regular file; choose another directory")
		}
		existing, e := os.ReadFile(notice)
		if e != nil {
			return "", e
		}
		if !bytes.Equal(existing, license) {
			return "", errors.New("existing restic license differs; choose another directory")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	} else {
		f, e := os.OpenFile(notice, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return "", e
		}
		_, e = f.Write(license)
		if e == nil {
			e = f.Sync()
		}
		e = errors.Join(e, f.Close())
		if e != nil {
			return "", e
		}
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	if e = publish(f.Name(), target); e != nil {
		return "", fmt.Errorf("publish verified restic without replacing an existing file; choose a trusted local directory with rename support (Unix also requires hard links): %w", e)
	}
	return target, nil
}
