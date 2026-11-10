package check

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/katharinasick/aocn/cli/internal/kubectl"
)

// Job starts a run of the CronJob and passes if it completes. On failure it doesn't name the
// CronJob, so finding it stays part of the challenge. The error is only for Ctrl+C.
func Job(ctx context.Context, out io.Writer, cronJob Source) (Result, error) {
	job := fmt.Sprintf("verify-%s-%d", cronJob.Name, time.Now().Unix())
	if _, err := kubectl.Run(ctx, "create", "job", "-n", cronJob.Namespace, "--from=cronjob/"+cronJob.Name, job); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return report(out, Result{Got: err.Error()},
			fmt.Sprintf("a scheduled job in %s couldn't start: %s", cronJob.Namespace, err)), nil
	}
	defer cleanup(func(ctx context.Context) {
		kubectl.Run(ctx, "delete", "job", "-n", cronJob.Namespace, job, "--wait=false")
	})

	succeeded, err := waitForJob(ctx, cronJob.Namespace, job, 60*time.Second)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	r := Result{Passed: succeeded, Got: "completed"}
	if !succeeded {
		r.Got = "failed"
	}
	if err != nil {
		r.Got = err.Error()
	}
	if succeeded {
		return report(out, r, fmt.Sprintf("a fresh %s run completes", cronJob.Name)), nil
	}
	return report(out, r, fmt.Sprintf("a scheduled job in %s doesn't complete", cronJob.Namespace)), nil
}

// waitForJob returns whether the job succeeded, once it succeeded or failed or timeout passed.
func waitForJob(ctx context.Context, namespace, job string, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := kubectl.Run(ctx, "get", "job", "-n", namespace, job,
			"-o", "jsonpath={.status.succeeded},{.status.failed}")
		if err == nil {
			succeeded, failed, _ := strings.Cut(out, ",")
			if succeeded != "" && succeeded != "0" {
				return true, nil
			}
			if failed != "" && failed != "0" {
				return false, nil
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return false, fmt.Errorf("didn't finish within %s", timeout)
}
