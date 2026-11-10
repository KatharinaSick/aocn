// Package check is the check engine behind aocn verify and aocn check. Each check sends real
// traffic or starts real work in the cluster, prints one line and returns its result.
package check

import (
	"errors"
	"fmt"
	"io"
)

// ErrFailed means a check ran and didn't pass. Its line is already printed.
var ErrFailed = errors.New("check failed")

// Result is one check's outcome: whether it passed, and what it got.
type Result struct {
	Passed bool
	Got    string
}

// report prints a check's line and returns its result.
func report(out io.Writer, r Result, message string) Result {
	if r.Passed {
		fmt.Fprintf(out, "  ✅ %s\n", message)
	} else {
		fmt.Fprintf(out, "  ❌ %s\n", message)
	}
	return r
}
