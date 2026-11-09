// check-pages checks door pages (page.yaml) with exactly the rules the API reads them by, so
// a page the API can't read fails a pull request in the challenges repository instead of
// showing up as a stuck door when it opens.
//
//	go run ./cmd/check-pages path/to/page.yaml ...
package main

import (
	"fmt"
	"os"

	"github.com/katharinasick/aocn/api/internal/doors"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: check-pages page.yaml ...")
		os.Exit(2)
	}
	failed := false
	for _, path := range os.Args[1:] {
		data, err := os.ReadFile(path)
		if err == nil {
			_, err = doors.ParsePage(data)
		}
		if err != nil {
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
