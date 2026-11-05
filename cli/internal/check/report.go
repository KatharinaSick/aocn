// Package check is the check engine behind `aocn check`. Each check sends real traffic or
// starts real work in the cluster, prints one line and returns ErrFailed if it failed.
package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrFailed means the check ran and didn't pass. Its line is already printed.
var ErrFailed = errors.New("check failed")

// ResultsEnv names a file that every check appends its result to, one JSON object per line.
// `aocn verify` sets it to count the failed checks.
const ResultsEnv = "AOCN_RESULTS"

// Result is one check's outcome, as written to the results file.
type Result struct {
	Check  string   `json:"check"`
	Args   []string `json:"args"`
	Passed bool     `json:"passed"`
	Got    string   `json:"got,omitempty"`
}

// report prints a check's line, records its result and returns ErrFailed if it didn't pass.
func report(out io.Writer, r Result, message string) error {
	if r.Passed {
		fmt.Fprintf(out, "  ✅ %s\n", message)
	} else {
		fmt.Fprintf(out, "  ❌ %s\n", message)
	}
	if err := record(r); err != nil {
		return err
	}
	if !r.Passed {
		return ErrFailed
	}
	return nil
}

func record(r Result) error {
	path := os.Getenv(ResultsEnv)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("recording the result: %w", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(r)
}

// ReadResults reads the results file that the checks of one run appended to.
func ReadResults(path string) ([]Result, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []Result
	dec := json.NewDecoder(f)
	for dec.More() {
		var r Result
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("reading the results: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}
