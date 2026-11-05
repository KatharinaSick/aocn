// Package cluster creates and starts the cluster every door runs on. It's the only package
// that knows the cluster runs on kind.
package cluster

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/katharinasick/aocn/cli/internal/kubectl"
	"github.com/katharinasick/aocn/cli/internal/run"
)

// Name is the kind cluster's name. Its control plane is reachable as Name+"-control-plane".
const Name = "aocn"

// nodeImage is the Kubernetes version every door runs on. kubectl in the devcontainer
// matches it.
const nodeImage = "kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed"

// Recreate deletes the cluster and creates a new one, so every door starts from the same
// clean cluster. config is the door's kind config, or "" for a plain cluster. The name and
// the node image are set here, whatever the config says.
func Recreate(ctx context.Context, out io.Writer, config string) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	if err := run.Stream(ctx, out, "kind", "delete", "cluster", "--name", Name); err != nil {
		return err
	}

	args := []string{"create", "cluster", "--name", Name, "--image", nodeImage}
	if config != "" {
		args = append(args, "--config", config)
	}
	if err := run.Stream(ctx, out, "kind", args...); err != nil {
		return err
	}
	return waitForAPIServer(ctx)
}

// Start starts the existing cluster after a container restart, or creates it with config if
// there is none. It doesn't reset anything in it.
func Start(ctx context.Context, out io.Writer, config string) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	clusters, err := run.Output(ctx, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(clusters), Name) {
		return Recreate(ctx, out, config)
	}

	nodes, err := run.Output(ctx, "kind", "get", "nodes", "--name", Name)
	if err != nil {
		return err
	}
	if _, err := run.Output(ctx, "docker", append([]string{"start"}, strings.Fields(nodes)...)...); err != nil {
		return err
	}
	if _, err := run.Output(ctx, "kind", "export", "kubeconfig", "--name", Name); err != nil {
		return err
	}
	return waitForAPIServer(ctx)
}

// preflight fails with a clear message if Docker can't run the cluster.
func preflight(ctx context.Context) error {
	// Rootless container runtimes map root to a normal user. The cluster needs real root. Outside Linux there's no uid_map, and Docker runs in a VM anyway.
	if uidMap, err := os.ReadFile("/proc/self/uid_map"); err == nil {
		if fields := strings.Fields(string(uidMap)); len(fields) < 3 || fields[1] != "0" || fields[2] != "4294967295" {
			return fmt.Errorf("this container runs rootless, so the cluster can't start.\n" +
				"Use Docker Desktop, OrbStack, Colima or rootful podman (podman machine set --rootful)")
		}
	}

	// On a container start, Docker can still be starting up.
	if err := poll(ctx, 60*time.Second, func() bool {
		_, err := run.Output(ctx, "docker", "info")
		return err == nil
	}); err != nil {
		return fmt.Errorf("Docker isn't running")
	}
	return nil
}

func waitForAPIServer(ctx context.Context) error {
	if err := poll(ctx, 120*time.Second, func() bool {
		_, err := kubectl.Run(ctx, "get", "--raw", "/readyz")
		return err == nil
	}); err != nil {
		return fmt.Errorf("the cluster's API server didn't get ready")
	}
	return nil
}

// poll calls ok every second until it returns true or timeout passed.
func poll(ctx context.Context, timeout time.Duration, ok func() bool) error {
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}
