package main

import (
	"context"
	"github.com/10/discord-cli/internal/cli"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Run(ctx, args, stdin, stdout, stderr, cli.Options{})
}
