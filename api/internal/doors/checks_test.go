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
		"empty":               "",
		"no checks":           "[]",
		"not a list":          "ready: [apps]",
		"unknown kind":        "- wait: apps",
		"unknown field":       "- http: {method: GET, to: a/b, from: a/new-pod, expect: 2xx, timeout: 5}",
		"two kinds":           "- {ready: [apps], job: apps/cronjob/x}",
		"no kind":             "- {}",
		"empty ready":         "- ready: []",
		"blank namespace":     "- ready: ['']",
		"lowercase method":    "- http: {method: get, to: a/b, from: a/new-pod, expect: 2xx}",
		"no to":               "- http: {method: GET, from: a/new-pod, expect: 2xx}",
		"no from":             "- http: {method: GET, to: a/b, expect: 2xx}",
		"bad expect":          "- http: {method: GET, to: a/b, from: a/new-pod, expect: ok}",
		"duplicate":           "- job: apps/cronjob/x\n- job: apps/cronjob/x",
		"two documents":       "- job: apps/cronjob/x\n---\n- job: apps/cronjob/y",
		"titles are no use":   "- title: Something\n  job: apps/cronjob/x",
		"login, no issuer":    "- login: {client: frontend, from: apps/new-pod, expect: 2xx}",
		"login, no client":    "- login: {issuer: auth/idp/realms/apps, from: apps/new-pod, expect: 2xx}",
		"login, no from":      "- login: {issuer: auth/idp/realms/apps, client: frontend, expect: 2xx}",
		"login, no expect":    "- login: {issuer: auth/idp/realms/apps, client: frontend, from: apps/new-pod}",
		"login, to alone":     "- login: {issuer: auth/idp/realms/apps, client: frontend, from: apps/new-pod, to: apps/api/items, expect: 2xx}",
		"login, method alone": "- login: {issuer: auth/idp/realms/apps, client: frontend, from: apps/new-pod, method: GET, expect: 2xx}",
		"login and http":      "- {job: apps/cronjob/x, login: {issuer: a/b/c, client: x, from: a/new-pod, expect: 2xx}}",
	}
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			if checks, err := ParseChecks([]byte(yaml)); err == nil {
				t.Errorf("got %+v, want an error", checks)
			}
		})
	}
}

func TestParseChecksWithGoals(t *testing.T) {
	checks, err := ParseChecks([]byte(`
- ready: [apps]
- goal: 2
  checks:
    - http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: 2xx}
    - job: apps/cronjob/cleanup
- goal: 1
  checks:
    - http: {method: GET, to: apps/api/items, from: apps/new-pod, expect: blocked}
`))
	if err != nil {
		t.Fatal(err)
	}
	var goals []int
	for _, c := range checks {
		goals = append(goals, c.Goal)
	}
	if len(checks) != 4 || goals[0] != 0 || goals[1] != 2 || goals[2] != 2 || goals[3] != 1 || checks[2].Job == "" {
		t.Errorf("got %+v", checks)
	}
	// The same check under a goal or without one is the same check.
	plain, _ := ParseChecks([]byte("- job: apps/cronjob/cleanup"))
	if plain[0].ID != checks[2].ID {
		t.Errorf("a goal changed the ID")
	}
	if err := Goals(checks, 2); err != nil {
		t.Error(err)
	}
	if err := Goals(checks, 1); err == nil {
		t.Error("goal 2 of a one-line mission passed")
	}
}

func TestParseChecksRefusesBrokenGoals(t *testing.T) {
	for name, yaml := range map[string]string{
		"no checks":       "- goal: 1\n  checks: []",
		"no goal":         "- checks:\n    - job: a/cronjob/x",
		"goal zero":       "- goal: 0\n  checks:\n    - job: a/cronjob/x",
		"check beside":    "- goal: 1\n  job: a/cronjob/y\n  checks:\n    - job: a/cronjob/x",
		"ready in a goal": "- goal: 1\n  checks:\n    - ready: [a]",
		"nested goal":     "- goal: 1\n  checks:\n    - goal: 2\n      checks:\n        - job: a/cronjob/x",
		"broken check":    "- goal: 1\n  checks:\n    - http: {method: GET}",
	} {
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
	// Under a goal, it's named by its goal and its place there.
	_, err = ParseChecks([]byte("- ready: [a]\n- goal: 2\n  checks:\n    - job: a/cronjob/x\n    - http: {method: GET}"))
	if err == nil || !strings.Contains(err.Error(), "goal 2, check 2") {
		t.Errorf("got %v, want it to name goal 2, check 2", err)
	}
}

func TestParseLoginChecks(t *testing.T) {
	checks, err := ParseChecks([]byte(`
- login: {issuer: auth/idp/realms/apps, client: frontend, from: apps/deploy/frontend, expect: 2xx}
- login: {issuer: auth/idp/realms/apps, client: frontend, from: apps/new-pod, method: GET, to: apps/api/items, expect: blocked}
- login: {issuer: auth/idp/realms/apps, client: worker, secret: s3cret, from: apps/new-pod, expect: 2xx}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 3 || checks[0].Login.Client != "frontend" || checks[1].Login.To != "apps/api/items" || checks[2].Login.Secret != "s3cret" {
		t.Errorf("got %+v", checks)
	}
	if checks[0].ID == checks[1].ID {
		t.Error("two different login checks have the same ID")
	}
}

// Checks without a login keep the IDs they had before login checks existed, so a result sent
// for an open door still names its check.
func TestCheckIDsWithoutLoginAreUnchanged(t *testing.T) {
	checks, err := ParseChecks([]byte(`- http: {method: GET, to: apps/api/items, from: apps/deploy/frontend, expect: 2xx}`))
	if err != nil {
		t.Fatal(err)
	}
	if checks[0].ID != "a66d98e2f2f5d4dd" {
		t.Errorf("ID %s, want a66d98e2f2f5d4dd as before", checks[0].ID)
	}
}
