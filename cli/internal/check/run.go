package check

import (
	"context"
	"fmt"
	"io"
)

// Check is one of a door's checks, as the API hands it out. Exactly one of Ready, HTTP and
// Job is set. Checks only ever come from the API, never from the player's folder, so editing
// files can't make a door pass.
type Check struct {
	// ID names the check when its result is sent back.
	ID    string     `json:"id"`
	Ready []string   `json:"ready,omitempty"`
	HTTP  *HTTPCheck `json:"http,omitempty"`
	Job   string     `json:"job,omitempty"`
}

// HTTPCheck is a request from a workload, with what it should get. See aocn check http.
type HTTPCheck struct {
	Method string `json:"method"`
	To     string `json:"to"`
	From   string `json:"from"`
	Expect string `json:"expect"`
	Data   string `json:"data,omitempty"`
}

// Outcome is one check's result, as sent to the API.
type Outcome struct {
	ID     string `json:"id"`
	Passed bool   `json:"passed"`
	Got    string `json:"got,omitempty"`
}

// NotRun is what a check got when it didn't run, because a ready check before it failed.
const NotRun = "not run"

// step runs one check, once it's known what it is.
type step func(ctx context.Context, out io.Writer) (Result, error)

// Run runs the checks in order and returns one outcome per check. A ready check that fails
// stops the run: the checks after it would only fail because the workloads are still
// starting, so they count as failed without running. The error is only for Ctrl+C.
func Run(ctx context.Context, out io.Writer, checks []Check) ([]Outcome, error) {
	return run(ctx, out, checks, plan)
}

func run(ctx context.Context, out io.Writer, checks []Check, plan func(Check) (step, error)) ([]Outcome, error) {
	outcomes := make([]Outcome, 0, len(checks))
	stopped := false
	for _, c := range checks {
		if stopped {
			outcomes = append(outcomes, Outcome{ID: c.ID, Got: NotRun})
			continue
		}
		var r Result
		s, err := plan(c)
		if err != nil {
			// The door's fault, or an aocn too old for it. Either way the check didn't pass.
			r = report(out, Result{Got: err.Error()}, err.Error())
		} else if r, err = s(ctx, out); err != nil {
			return nil, err
		}
		outcomes = append(outcomes, Outcome{ID: c.ID, Passed: r.Passed, Got: r.Got})
		if c.Ready != nil && !r.Passed {
			stopped = true
		}
	}
	return outcomes, nil
}

// plan works out what a check does, with the same rules as aocn check.
func plan(c Check) (step, error) {
	switch {
	case c.Ready != nil:
		return func(ctx context.Context, out io.Writer) (Result, error) {
			return Ready(ctx, out, c.Ready)
		}, nil

	case c.HTTP != nil:
		target, err := ParseTarget(c.HTTP.To)
		if err != nil {
			return nil, brokenCheck(err)
		}
		source, err := ParseSource(c.HTTP.From)
		if err != nil {
			return nil, brokenCheck(err)
		}
		expect, err := ParseExpect(c.HTTP.Expect)
		if err != nil {
			return nil, brokenCheck(err)
		}
		return func(ctx context.Context, out io.Writer) (Result, error) {
			return HTTP(ctx, out, c.HTTP.Method, source, target, c.HTTP.Data, expect)
		}, nil

	case c.Job != "":
		source, err := ParseSource(c.Job)
		if err != nil || source.Kind != KindCronJob {
			return nil, brokenCheck(fmt.Errorf("job must be NAMESPACE/cronjob/NAME, got %q", c.Job))
		}
		return func(ctx context.Context, out io.Writer) (Result, error) {
			return Job(ctx, out, source)
		}, nil
	}
	// A kind of check this aocn doesn't know: the API is newer.
	return nil, fmt.Errorf("this door has a check your aocn doesn't know yet. Update aocn and try again")
}

func brokenCheck(err error) error {
	return fmt.Errorf("one of this door's checks is broken, which is not your fault: %w", err)
}

// Failed counts the checks that ran and didn't pass. Checks that didn't run aren't counted:
// the one that stopped the run is what to fix first.
func Failed(outcomes []Outcome) int {
	n := 0
	for _, o := range outcomes {
		if !o.Passed && o.Got != NotRun {
			n++
		}
	}
	return n
}
