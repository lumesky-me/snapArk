package cli

import (
	"io"
	"sync"
)

// Backend progress and diagnostics can arrive on separate pipe-copy goroutines.
// Share one lock even when both streams use the same non-thread-safe writer.
type outputWriter struct {
	writer io.Writer
	mutex  *sync.Mutex
}

func (w *outputWriter) Write(p []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.writer.Write(p)
}
