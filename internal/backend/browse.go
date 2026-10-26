package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
)

// Browse reads one directory at a time and retains at most limit+1 nodes.
// after is a stable lexical cursor, not an offset into a huge in-memory tree.
func (r Repository) Browse(ctx context.Context, id, dir, after, query string, limit int) ([]Node, bool, error) {
	if !snapshotID.MatchString(id) || limit < 1 || limit > 100 || !strings.HasPrefix(dir, "/") || path.Clean(dir) != dir {
		return nil, false, errors.New("invalid snapshot directory or page limit")
	}
	r.JSON = true
	r.Quiet = true
	r.ReadOnly = true
	c := r.command(ctx, "ls", id, dir)
	c.Stdout = nil
	pipe, e := c.StdoutPipe()
	if e != nil {
		return nil, false, e
	}
	if e = c.Start(); e != nil {
		return nil, false, e
	}
	nodes := []Node{}
	d := json.NewDecoder(pipe)
	for {
		var n Node
		e = d.Decode(&n)
		if e == io.EOF {
			e = nil
			break
		}
		if e != nil {
			_ = c.Process.Kill()
			break
		}
		if n.Path == "" || n.Path == dir || path.Dir(n.Path) != dir || n.Path <= after || !strings.Contains(strings.ToLower(path.Base(n.Path)), strings.ToLower(query)) {
			continue
		}
		nodes = append(nodes, n)
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
		if len(nodes) > limit+1 {
			nodes = nodes[:limit+1]
		}
	}
	_, _ = io.Copy(io.Discard, pipe)
	runErr := c.Wait()
	if e = errors.Join(e, runErr); e != nil {
		return nil, false, e
	}
	more := len(nodes) > limit
	if more {
		nodes = nodes[:limit]
	}
	return nodes, more, nil
}
