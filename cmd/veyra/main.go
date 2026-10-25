package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/lumesky-me/veyra/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := (cli.App{}).Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
