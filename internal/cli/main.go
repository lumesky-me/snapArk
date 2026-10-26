package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Main serves both the snapark command and its legacy veyra entry point.
func Main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := (App{}).Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
