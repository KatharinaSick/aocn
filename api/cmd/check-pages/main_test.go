package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A goal that isn't a line of the mission next to it fails the check.
func TestCheckRefusesAGoalBeyondTheMission(t *testing.T) {
	dir := t.TempDir()
	page := "title: T\nstory: S.\nmission: [Only goal]\npola: P.\nmaintainer: {name: N, role: r, url: 'https://example.com'}\n"
	if err := os.WriteFile(filepath.Join(dir, "page.yaml"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := filepath.Join(dir, "checks.yaml")
	for goal, ok := range map[string]bool{"1": true, "2": false} {
		yaml := "- goal: " + goal + "\n  checks:\n    - job: a/cronjob/x\n"
		if err := os.WriteFile(checks, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := check(checks); (err == nil) != ok {
			t.Errorf("goal %s: got %v", goal, err)
		}
	}
}
