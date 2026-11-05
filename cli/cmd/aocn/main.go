package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/katharinasick/aocn/cli/internal/check"
	"github.com/katharinasick/aocn/cli/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = ""

func main() {
	// Ctrl+C cancels the context, so commands can clean up what they created in the cluster.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.NewRootCmd(version).ExecuteContext(ctx); err != nil {
		// A failed check already printed its line, and Ctrl+C needs no message.
		if !errors.Is(err, check.ErrFailed) && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
