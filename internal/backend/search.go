package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func (r Repository) Search(ctx context.Context, ref, query string) ([]Node, error) {
	if query == "" {
		return nil, errors.New("search requires a nonempty query")
	}
	snapshot, e := r.Resolve(ctx, ref)
	if e != nil {
		return nil, e
	}
	r.JSON = true
	r.Quiet = false
	r.ReadOnly = true
	c := r.command(ctx, "ls", snapshot.ID)
	c.Stdout = nil
	pipe, e := c.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = c.Start(); e != nil {
		return nil, e
	}
	result := []Node{}
	decoder := json.NewDecoder(pipe)
	for {
		var n Node
		e = decoder.Decode(&n)
		if e == io.EOF {
			e = nil
			break
		}
		if e != nil {
			_ = c.Process.Kill()
			break
		}
		if n.Path != "" && strings.Contains(strings.ToLower(n.Path), strings.ToLower(query)) {
			result = append(result, n)
		}
	}
	_, _ = io.Copy(io.Discard, pipe)
	runErr := c.Wait()
	if runErr != nil {
		return nil, runErr
	}
	return result, e
}
