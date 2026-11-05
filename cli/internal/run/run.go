// Package run runs the tools aocn builds on: kubectl, kind and docker.
package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Output runs name with args and returns its stdout. If it fails, the error contains what it
// wrote to stderr.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	return OutputWithInput(ctx, "", name, args...)
}

// OutputWithInput is Output with stdin, for example a manifest for `kubectl apply -f -`.
func OutputWithInput(ctx context.Context, stdin, name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("%s", msg)
		}
		return stdout.String(), fmt.Errorf("%s: %w", name, err)
	}
	return stdout.String(), nil
}

// Stream runs name with args and passes its output on to out, for long steps where the
// player should see progress.
func Stream(ctx context.Context, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
