// Command censys_go is an interactive and scriptable client for the Censys API,
// built on the official SDK at https://github.com/censys/censys-sdk-go (MIT).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/cli"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// Exit codes: 2 for a malformed command line, 130 for an interrupt, 1 otherwise.
const (
	exitOK     = 0
	exitError  = 1
	exitUsage  = 2
	exitSignal = 130
)

func main() {
	os.Exit(run())
}

func run() int {
	// A single Ctrl+C cancels in-flight work so partial results still get
	// written; a second one is left to the runtime to abort hard.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.Run(ctx, cli.Env{
		In:      os.Stdin,
		Out:     os.Stdout,
		Msg:     os.Stderr,
		Version: version,
	}, os.Args[1:])

	switch {
	case err == nil:
		return exitOK
	case cli.Interrupted(ctx, err):
		// Partial results have already been written and flushed; the non-zero
		// status is what tells a calling script the run was cut short.
		fmt.Fprintln(os.Stderr, "interrupted")
		return exitSignal
	case errors.Is(err, cli.ErrUsage):
		fmt.Fprintf(os.Stderr, "[Error] %v\n", err)
		return exitUsage
	default:
		fmt.Fprintf(os.Stderr, "[Error] %s\n", censysx.Explain(err))
		return exitError
	}
}
