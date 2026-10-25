//go:build !windows

package backend

func canonicalExisting(path string) (string, error) { return path, nil }
