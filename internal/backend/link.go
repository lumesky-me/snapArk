package backend

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// validateLinks resolves components in filesystem order, before cleaning '..'.
// Lexical cleaning alone can miss an escape through a second relative symlink.
func validateLinks(links map[string]string) error {
	folded := make(map[string]string, len(links))
	for p, target := range links {
		folded[pathKey(p)] = target
	}
	for p, target := range links {
		var stack []string
		parent := strings.TrimPrefix(path.Dir(p), "/")
		if parent != "" && parent != "." {
			stack = strings.Split(parent, "/")
		}
		pending := strings.Split(target, "/")
		hops := 0
		for len(pending) > 0 {
			part := pending[0]
			pending = pending[1:]
			switch part {
			case "", ".":
				continue
			case "..":
				if len(stack) == 0 {
					return fmt.Errorf("symbolic link chain escapes restore root: %q", p)
				}
				stack = stack[:len(stack)-1]
				continue
			}
			candidate := "/" + strings.Join(append(append([]string{}, stack...), part), "/")
			if next, ok := folded[pathKey(candidate)]; ok {
				hops++
				if hops > 64 {
					return fmt.Errorf("symbolic link cycle or excessive chain at %q", p)
				}
				pending = append(strings.Split(next, "/"), pending...)
			} else {
				stack = append(stack, part)
			}
		}
	}
	return nil
}

// Use a stable representative of each Unicode simple-fold cycle. Lowercasing
// alone misses aliases such as Greek final sigma and the long Latin s.
func pathKey(p string) string {
	return strings.Map(func(r rune) rune {
		best := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < best {
				best = next
			}
		}
		return best
	}, p)
}
