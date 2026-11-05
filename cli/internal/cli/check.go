package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katharinasick/aocn/cli/internal/check"
)

// newCheckCmd is the check engine that doors' verify.sh scripts call, one check per line:
//
//	aocn check http GET apps/api/items --from apps/deploy/frontend --expect 2xx
func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run one check (used by a door's verify.sh)",
		Long: `check runs one check against the cluster, prints one line and exits with 1 if it
failed. Doors call it from their verify.sh.

Workloads are named NAMESPACE/KIND/NAME, like kubectl does:
  apps/deploy/frontend   inside the Deployment's pod
  apps/cronjob/cleanup   a new pod with the CronJob's labels
  apps/new-pod           a new pod in the namespace, with no special labels`,
		Hidden: true,
	}

	cmd.AddCommand(newCheckHTTPCmd(), newCheckJobCmd(), newCheckReadyCmd())
	return cmd
}

func newCheckHTTPCmd() *cobra.Command {
	var from, expect, data string
	cmd := &cobra.Command{
		Use:   "http METHOD NAMESPACE/SERVICE[:PORT]/PATH|test-server[/PATH] --from WORKLOAD --expect RESULT",
		Short: "Sends a request from a workload and passes if the result is the expected one",
		Long: `http sends a real request from a workload to a service (port 8080 unless given) and
passes if the result matches --expect:
  200, 403, …   exactly this status code
  2xx, 4xx, …   any status code in this class
  blocked       refused (403) or no connection, so a door can be solved either way

The target test-server[/PATH] is a server the check creates in a namespace of its own, with no
policies. Only the sender's policies decide, so it shows whether a workload can reach places
it doesn't need. A new pod sends to it without the open egress it gets elsewhere.`,
		Example: `  aocn check http GET apps/api/items --from apps/deploy/frontend --expect 2xx
  aocn check http POST apps/api/items --data '{"name":"test"}' --from apps/new-pod --expect blocked
  aocn check http GET test-server --from apps/cronjob/cleanup --expect blocked`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := check.ParseTarget(args[1])
			if err != nil {
				return err
			}
			source, err := check.ParseSource(from)
			if err != nil {
				return err
			}
			exp, err := check.ParseExpect(expect)
			if err != nil {
				return err
			}
			all := append(append([]string{}, args...), "--from", from, "--expect", expect, "--data", data)
			return check.HTTP(cmd.Context(), cmd.OutOrStdout(), all, args[0], source, target, data, exp)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the workload that sends the request")
	cmd.Flags().StringVar(&expect, "expect", "", "the expected result: a status code, a class like 2xx, or blocked")
	cmd.Flags().StringVar(&data, "data", "", "the request body")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("expect")
	return cmd
}

func newCheckJobCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "job NAMESPACE/cronjob/NAME",
		Short: "Starts a run of the CronJob and passes if it completes",
		Long: `job starts a run of the CronJob and passes if it completes. On failure it doesn't
name the CronJob, so finding it stays part of the challenge.`,
		Example: `  aocn check job apps/cronjob/cleanup`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := check.ParseSource(args[0])
			if err != nil || source.Kind != check.KindCronJob {
				return fmt.Errorf("job must be NAMESPACE/cronjob/NAME, got %q", args[0])
			}
			return check.Job(cmd.Context(), cmd.OutOrStdout(), args, source)
		},
	}
}

func newCheckReadyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ready NAMESPACE...",
		Short: "Passes once every Deployment in the namespaces is ready",
		Long: `ready waits up to a minute for every Deployment in the namespaces. Run it first, so
the other checks don't fail because the workloads are still starting.`,
		Example: `  aocn check ready apps db || exit 1`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return check.Ready(cmd.Context(), cmd.OutOrStdout(), args)
		},
	}
}
