// Package check is the check engine behind aocn verify and aocn check. Each check sends real
// traffic or starts real work in the cluster and returns its result, with a line that says
// what it found.
package check

import (
	"errors"
	"fmt"
	"io"
)

// ErrFailed means a check ran and didn't pass. Its line is already printed.
var ErrFailed = errors.New("check failed")

// Result is one check's outcome: whether it passed, what it got, and the line that says so.
type Result struct {
	Passed  bool
	Got     string
	Message string
}

// said returns r with its line.
func said(r Result, message string) Result {
	r.Message = message
	return r
}

// Print prints a result's line.
func Print(out io.Writer, r Result) {
	mark(out, r.Passed, r.Message)
}

// mark prints one line with ✅ or ❌.
func mark(out io.Writer, passed bool, line string) {
	if passed {
		fmt.Fprintf(out, "  ✅ %s\n", line)
	} else {
		fmt.Fprintf(out, "  ❌ %s\n", line)
	}
}
