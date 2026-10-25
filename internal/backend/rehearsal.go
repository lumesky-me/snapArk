package backend

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// SampleFiles scans metadata but retains only the lexicographically first bounded
// eligible regular files. It does not claim to validate symlinks or metadata.
func (r Repository) SampleFiles(ctx context.Context, id string, count int, budget int64) ([]Node, error) {
	if count < 1 || count > 32 || budget < 1 || budget > 1<<30 {
		return nil, errors.New("sample limits: 1..32 files and 1..1073741824 bytes")
	}
	r.JSON = true
	r.Quiet = true
	c := r.command(ctx, "ls", id)
	c.Stdout = nil
	pipe, e := c.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = c.Start(); e != nil {
		return nil, e
	}
	selected := []Node{}
	d := json.NewDecoder(pipe)
	for {
		var node Node
		e = d.Decode(&node)
		if e == io.EOF {
			e = nil
			break
		}
		if e != nil {
			_ = c.Process.Kill()
			break
		}
		if node.Type != "file" || node.Size > uint64(budget) {
			continue
		}
		if err := SafeNode(node); err != nil {
			continue
		}
		selected = append(selected, node)
		sort.Slice(selected, func(i, j int) bool { return selected[i].Path < selected[j].Path })
		if len(selected) > count {
			selected = selected[:count]
		}
	}
	_, _ = io.Copy(io.Discard, pipe)
	runErr := c.Wait()
	if runErr != nil {
		return nil, runErr
	}
	if e != nil {
		return nil, e
	}
	result := []Node{}
	var total uint64
	for _, node := range selected {
		if total+node.Size <= uint64(budget) {
			result = append(result, node)
			total += node.Size
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no portable regular files fit the sample budget")
	}
	return result, nil
}

// RecoverSample streams authenticated restic dump output into a generated private
// filename, then independently rereads it. Archive paths never become output paths.
func (r Repository) RecoverSample(ctx context.Context, id string, node Node, target string) error {
	if node.Size > 1<<30 {
		return errors.New("sample file exceeds limit")
	}
	f, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	r.JSON = false
	r.Quiet = true
	c := r.command(ctx, "dump", id, node.Path)
	c.Stdout = nil
	pipe, e := c.StdoutPipe()
	if e != nil {
		_ = f.Close()
		return e
	}
	if e = c.Start(); e != nil {
		_ = f.Close()
		return e
	}
	hash := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(f, hash), pipe, int64(node.Size)+1)
	if copyErr == io.EOF {
		copyErr = nil
	}
	if written > int64(node.Size) || copyErr != nil {
		_ = c.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, pipe)
	runErr := c.Wait()
	syncErr := f.Sync()
	closeErr := f.Close()
	if e = errors.Join(copyErr, runErr, syncErr, closeErr); e != nil {
		return e
	}
	if written != int64(node.Size) {
		return fmt.Errorf("restored sample length differs from snapshot metadata")
	}
	check, e := os.Open(target)
	if e != nil {
		return e
	}
	defer check.Close()
	actual := sha256.New()
	if _, e = io.Copy(actual, check); e != nil {
		return e
	}
	if string(actual.Sum(nil)) != string(hash.Sum(nil)) {
		return errors.New("restored sample hash mismatch")
	}
	return nil
}
