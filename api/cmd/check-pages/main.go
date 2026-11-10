// check-pages checks a door's page.yaml and checks.yaml with exactly the rules the API reads
// them by, so a file the API can't read fails a pull request in the challenges repository
// instead of showing up as a stuck door, or a door nobody can verify, when it opens.
//
//	go run ./cmd/check-pages path/to/page.yaml path/to/checks.yaml ...
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/katharinasick/aocn/api/internal/doors"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: check-pages page.yaml|checks.yaml ...")
		os.Exit(2)
	}
	failed := false
	for _, path := range os.Args[1:] {
		if err := check(path); err != nil {
			fmt.Fprintf(os.Stderr, "check-pages: %s: %v\n", path, err)
			failed = true
			continue
		}
		fmt.Printf("check-pages: %s is fine\n", path)
	}
	if failed {
		os.Exit(1)
	}
}

// check reads the file by its name, the way the API reads what the upload put in the bucket.
func check(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch filepath.Base(path) {
	case "page.yaml":
		_, err = doors.ParsePage(data)
	case "checks.yaml":
		_, err = doors.ParseChecks(data)
	default:
		err = fmt.Errorf("only page.yaml and checks.yaml can be checked")
	}
	return err
}
