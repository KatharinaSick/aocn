// Package kubectl runs kubectl against the current context.
package kubectl

import (
	"context"

	"github.com/katharinasick/aocn/cli/internal/run"
)

// Run runs kubectl with args and returns its stdout. If it fails, the error contains what
// kubectl wrote to stderr.
func Run(ctx context.Context, args ...string) (string, error) {
	return run.Output(ctx, "kubectl", args...)
}

// RunWithInput is Run with stdin, for example a manifest for `kubectl apply -f -`.
func RunWithInput(ctx context.Context, stdin string, args ...string) (string, error) {
	return run.OutputWithInput(ctx, stdin, "kubectl", args...)
}
