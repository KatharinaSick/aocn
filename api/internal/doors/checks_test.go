package doors

import (
	"strings"
	"testing"
)

const validChecks = `
- ready: [apps, db]
- http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: 2xx}
- http: {method: POST, to: apps/api/items, from: apps/new-pod, expect: blocked, data: '{"name":"test"}'}
- job: apps/cronjob/cleanup
`

func TestParseChecks(t *testing.T) {
	checks, err := ParseChecks([]byte(validChecks))
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 4 {
		t.Fatalf("got %d checks, want 4", len(checks))
	}
	if checks[0].Ready[1] != "db" || checks[1].HTTP.Expect != "2xx" || checks[2].HTTP.Data != `{"name":"test"}` || checks[3].Job != "apps/cronjob/cleanup" {
		t.Errorf("got %+v", checks)
	}
	for i, c := range checks {
		if len(c.ID) != 16 {
			t.Errorf("check %d: ID %q", i+1, c.ID)
		}
	}
}

// The ID is what the API trusts a result by, so any change to a check must change it, and
// nothing else may.
func TestCheckIDs(t *testing.T) {
	id := func(yaml string) string {
		t.Helper()
		checks, err := ParseChecks([]byte(yaml))
		if err != nil {
			t.Fatal(err)
		}
		return checks[0].ID
	}
	base := id(`- http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: 2xx}`)

	same := []string{
		"- http:\n    expect: 2xx\n    from: apps/deploy/frontend\n    to: apps/api/items\n    method: GET\n",
		`- {http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: "2xx"}}`,
	}
	for _, y := range same {
		if got := id(y); got != base {
			t.Errorf("%q: ID %s, want %s", y, got, base)
		}
	}

	changed := []string{
		`- http: {method: POST, to: apps/api/items, from: apps/deploy/frontend, expect: 2xx}`,
		`- http: {method: GET, to: apps/api/other, from: apps/deploy/frontend, expect: 2xx}`,
		`- http: {method: GET, to: apps/api/items, from: apps/new-pod, expect: 2xx}`,
		`- http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: blocked}`,
		`- http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: 2xx, data: x}`,
	}
	for _, y := range changed {
		if got := id(y); got == base {
			t.Errorf("%q: same ID as the original", y)
		}
	}
}

func TestParseChecksRefuses(t *testing.T) {
	tests := map[string]string{
		"empty":             "",
		"no checks":         "[]",
		"not a list":        "ready: [apps]",
		"unknown kind":      "- wait: apps",
		"unknown field":     "- http: {method: GET, to: a/b, from: a/new-pod, expect: 2xx, timeout: 5}",
		"two kinds":         "- {ready: [apps], job: apps/cronjob/x}",
		"no kind":           "- {}",
		"empty ready":       "- ready: []",
		"blank namespace":   "- ready: ['']",
		"lowercase method":  "- http: {method: get, to: a/b, from: a/new-pod, expect: 2xx}",
		"no to":             "- http: {method: GET, from: a/new-pod, expect: 2xx}",
		"no from":           "- http: {method: GET, to: a/b, expect: 2xx}",
		"bad expect":        "- http: {method: GET, to: a/b, from: a/new-pod, expect: ok}",
		"duplicate":         "- job: apps/cronjob/x\n- job: apps/cronjob/x",
		"two documents":     "- job: apps/cronjob/x\n---\n- job: apps/cronjob/y",
		"titles are no use": "- title: Something\n  job: apps/cronjob/x",
	}
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			if checks, err := ParseChecks([]byte(yaml)); err == nil {
				t.Errorf("got %+v, want an error", checks)
			}
		})
	}
}

func TestParseChecksNamesTheBrokenCheck(t *testing.T) {
	_, err := ParseChecks([]byte("- job: apps/cronjob/x\n- http: {method: GET, to: a/b, from: a/new-pod, expect: ok}"))
	if err == nil || !strings.Contains(err.Error(), "check 2") {
		t.Errorf("got %v, want it to name check 2", err)
	}
}
