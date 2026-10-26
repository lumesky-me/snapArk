package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/lumesky-me/snapArk/internal/backend"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var errCancelled = errors.New("cancelled; no further changes made")

type prompts struct {
	context context.Context
	reader  *bufio.Reader
	out     io.Writer
	input   io.Reader
}

func (a App) prompts(ctx context.Context) (*prompts, error) {
	interactive := false
	if a.isTerminal != nil {
		interactive = a.isTerminal()
	} else if f, ok := a.In.(*os.File); ok {
		interactive = term.IsTerminal(int(f.Fd()))
	}
	if !interactive {
		return nil, errors.New("this command requires an interactive terminal; use profile, init, snapshots, files and restore for scripts")
	}
	return &prompts{ctx, bufio.NewReaderSize(a.In, 4096), a.Out, a.In}, nil
}
func (p *prompts) line(label string) (string, error) {
	if e := p.context.Err(); e != nil {
		return "", e
	}
	fmt.Fprint(p.out, label+" ")
	type answer struct {
		s string
		e error
	}
	ch := make(chan answer, 1)
	go func() {
		s, e := p.reader.ReadString('\n')
		if len(s) > 32768 {
			e = errors.New("input exceeds 32 KiB")
		}
		ch <- answer{s, e}
	}()
	select {
	case <-p.context.Done():
		return "", p.context.Err()
	case a := <-ch:
		if a.e != nil {
			return "", errors.Join(errCancelled, a.e)
		}
		s := strings.TrimSpace(a.s)
		if s == "q" || s == "quit" {
			return "", errCancelled
		}
		return s, nil
	}
}
func (p *prompts) yes(label string) (bool, error) {
	s, e := p.line(label + " [y/N]")
	return strings.EqualFold(s, "y") || strings.EqualFold(s, "yes"), e
}
func (p *prompts) password() (value []byte, err error) {
	f, ok := p.input.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil, errors.New("secure password entry requires a terminal; alternatively provide --password-file")
	}
	fmt.Fprint(p.out, "Repository password (hidden): ")
	state, e := term.GetState(int(f.Fd()))
	if e != nil {
		return nil, e
	}
	defer term.Restore(int(f.Fd()), state)
	type answer struct {
		b []byte
		e error
	}
	ch := make(chan answer)
	go func() {
		b, e := term.ReadPassword(int(f.Fd()))
		select {
		case ch <- answer{b, e}:
		case <-p.context.Done():
			wipe(b)
		}
	}()
	select {
	case <-p.context.Done():
		return nil, p.context.Err()
	case a := <-ch:
		value, err = a.b, a.e
	}
	fmt.Fprintln(p.out)
	if err == nil && (len(value) == 0 || strings.ContainsAny(string(value), "\r\n\x00")) {
		err = errors.New("password must be nonempty and contain no line breaks")
	}
	if err != nil {
		wipe(value)
		value = nil
	}
	return
}
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Read a terminal credential once per CLI operation, rather than letting each
// metadata/verification subprocess ask again. Explicit files and piped/scripted
// input retain their existing behavior. All prompts go to stderr in JSON mode.
func (a App) repositoryPassword(ctx context.Context, r *backend.Repository, confirm bool) error {
	if r.PasswordFile != "" || len(r.PasswordInput) > 0 {
		return nil
	}
	f, ok := a.In.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	p := &prompts{context: ctx, out: a.Err, input: f}
	value, e := p.password()
	if e != nil {
		return e
	}
	if confirm {
		fmt.Fprintln(a.Err, "Confirm the new repository password.")
		again, e := p.password()
		match := bytes.Equal(value, again)
		wipe(again)
		if e != nil || !match {
			wipe(value)
			if e != nil {
				return e
			}
			return errors.New("passwords do not match; repository unchanged")
		}
	}
	r.PasswordInput = value
	return nil
}
