package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katharinasick/aocn/cli/internal/check"
)

// newCheckCmd runs one check by hand, the same way aocn verify runs a door's checks. It's
// for building doors: it saves nothing.
//
//	aocn check http GET apps/api/items --from apps/deploy/frontend --expect 2xx
func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run one check, for building doors",
		Long: `check runs one check against the cluster, prints one line and exits with 1 if it
failed. It's how a door's checks behave in aocn verify, one at a time, and saves nothing.

Workloads are named NAMESPACE/KIND/NAME, like kubectl does:
  apps/deploy/frontend   inside the Deployment's pod
  apps/cronjob/cleanup   a new pod with the CronJob's labels
  apps/new-pod           a new pod in the namespace, with no special labels`,
		Hidden: true,
	}

	cmd.AddCommand(newCheckHTTPCmd(), newCheckLoginCmd(), newCheckJobCmd(), newCheckReadyCmd())
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
			f := &check.Fixtures{}
			defer f.Close()
			return failedIfNot(cmd)(check.HTTP(cmd.Context(), f, args[0], source, target, data, exp))
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the workload that sends the request")
	cmd.Flags().StringVar(&expect, "expect", "", "the expected result: a status code, a class like 2xx, or blocked")
	cmd.Flags().StringVar(&data, "data", "", "the request body")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("expect")
	return cmd
}

func newCheckLoginCmd() *cobra.Command {
	var c check.LoginCheck
	cmd := &cobra.Command{
		Use:   "login NAMESPACE/SERVICE[:PORT]/PATH --client NAME --from WORKLOAD --expect RESULT",
		Short: "Logs in from a workload, and passes if the result is the expected one",
		Long: `login logs in from a workload as an OAuth client, at the OpenID Connect issuer at the
given address (port 8080 unless given), with the client credentials grant. It finds the token
endpoint through the issuer's /.well-known/openid-configuration.

With --secret, the workload logs in with the client's secret. Without it, it logs in with its
ServiceAccount token as a client assertion (RFC 7523), addressed to the issuer.

The login always comes from a new pod: with the ServiceAccount and labels of a Deployment's or
CronJob's pods, or as the namespace's default ServiceAccount for a new pod.

With --method and --to, it then sends that request with the access token, and --expect is
about its answer. Without them, --expect is about the login. A refused login counts as
blocked:
  200, 403, …   exactly this status code
  2xx, 4xx, …   any status code in this class
  blocked       a refused login, or a request refused (403) or with no connection`,
		Example: `  aocn check login auth/idp/realms/apps --client frontend --from apps/deploy/frontend --expect 2xx
  aocn check login auth/idp/realms/apps --client frontend --from apps/new-pod --method GET --to apps/api/items --expect blocked
  aocn check login auth/idp/realms/apps --client worker --secret s3cret --from apps/new-pod --expect 2xx`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c.Issuer = args[0]
			l, err := check.ParseLogin(c)
			if err != nil {
				return err
			}
			f := &check.Fixtures{}
			defer f.Close()
			return failedIfNot(cmd)(check.RunLogin(cmd.Context(), f, l))
		},
	}
	cmd.Flags().StringVar(&c.Client, "client", "", "the client to log in as")
	cmd.Flags().StringVar(&c.Secret, "secret", "", "the client's secret; without it, the workload's ServiceAccount token")
	cmd.Flags().StringVar(&c.From, "from", "", "the workload that logs in")
	cmd.Flags().StringVar(&c.Method, "method", "", "the method of the request after the login")
	cmd.Flags().StringVar(&c.To, "to", "", "where the request after the login goes: NAMESPACE/SERVICE[:PORT]/PATH")
	cmd.Flags().StringVar(&c.Expect, "expect", "", "the expected result: a status code, a class like 2xx, or blocked")
	_ = cmd.MarkFlagRequired("client")
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
			return failedIfNot(cmd)(check.Job(cmd.Context(), source))
		},
	}
}

func newCheckReadyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ready NAMESPACE...",
		Short: "Passes once every Deployment in the namespaces is ready",
		Long: `ready waits up to a minute for every Deployment in the namespaces. In a door's checks
it comes first: if it fails, aocn verify stops, so the other checks don't fail because the
workloads are still starting.`,
		Example: `  aocn check ready apps db`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return failedIfNot(cmd)(check.Ready(cmd.Context(), args))
		},
	}
}

// failedIfNot prints a check's line and turns its result into the command's exit: 1 if it
// didn't pass.
func failedIfNot(cmd *cobra.Command) func(check.Result, error) error {
	return func(r check.Result, err error) error {
		if err != nil {
			return err
		}
		check.Print(cmd.OutOrStdout(), r)
		if !r.Passed {
			return check.ErrFailed
		}
		return nil
	}
}
