//go:build !windows

package backend

import "context"

func canonicalExisting(path string) (string, error) { return path, nil }

func checkPathComponents(string) error              { return nil }
func checkSourceTree(context.Context, string) error { return nil }
