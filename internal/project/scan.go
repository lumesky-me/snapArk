package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type Fingerprint struct {
	Hash string
	Mode os.FileMode
	Size int64
}

type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

func Scan(ctx context.Context, root string) (map[string]Fingerprint, error) {
	files := make(map[string]Fingerprint)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		before, err := os.Lstat(p)
		if err != nil {
			return err
		}
		var hash string
		switch {
		case before.Mode().IsRegular():
			f, e := os.Open(p)
			if e != nil {
				return e
			}
			h := sha256.New()
			_, e = io.Copy(h, f)
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			hash = hex.EncodeToString(h.Sum(nil))
			after, e := os.Lstat(p)
			if e != nil {
				return e
			}
			if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				return fmt.Errorf("file changed while scanning: %s", p)
			}
		case before.Mode()&os.ModeSymlink != 0:
			hash, err = os.Readlink(p)
			if err != nil {
				return err
			}
		case before.IsDir():
		default:
			return fmt.Errorf("unsupported project entry: %s", p)
		}
		files[filepath.ToSlash(rel)] = Fingerprint{Hash: hash, Mode: before.Mode(), Size: before.Size()}
		return nil
	})
	return files, err
}

func Compare(before, after map[string]Fingerprint) []Change {
	result := []Change{}
	for p, b := range before {
		a, ok := after[p]
		if !ok {
			result = append(result, Change{Path: p, Kind: "deleted"})
		} else if a != b {
			result = append(result, Change{Path: p, Kind: "modified"})
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			result = append(result, Change{Path: p, Kind: "added"})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
