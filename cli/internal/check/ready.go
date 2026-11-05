package check

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/katharinasick/aocn/cli/internal/kubectl"
)

// Ready passes once every Deployment in the namespaces is ready, waiting up to a minute.
func Ready(ctx context.Context, out io.Writer, namespaces []string) error {
	for _, ns := range namespaces {
		if _, err := kubectl.Run(ctx, "rollout", "status", "deploy", "-n", ns, "--timeout=60s"); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return report(out, Result{Check: "ready", Args: namespaces, Got: err.Error()},
				fmt.Sprintf("the workloads in %s aren't ready. Check them with: kubectl get pods -n %s", ns, ns))
		}
	}
	return report(out, Result{Check: "ready", Args: namespaces, Passed: true},
		fmt.Sprintf("the workloads in %s are ready", strings.Join(namespaces, " and ")))
}
