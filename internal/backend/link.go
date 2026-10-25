package backend

import (
	"fmt"
	"path"
	"strings"
)

// validateLinks resolves components in filesystem order, before cleaning '..'.
// Lexical cleaning alone can miss an escape through a second relative symlink.
func validateLinks(links map[string]string) error {
	folded := make(map[string]string, len(links))
	for p, target := range links {
		folded[strings.ToLower(p)] = target
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
			if next, ok := folded[strings.ToLower(candidate)]; ok {
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
