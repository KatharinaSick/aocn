package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/cli/internal/api"
)

func TestDoorFromEnv(t *testing.T) {
	for value, want := range map[string]int{"": 0, "  ": 0, "1": 1, " 23 ": 23} {
		got, err := doorFromEnv(func(string) string { return value })
		if err != nil || got != want {
			t.Errorf("AOCN_DOOR=%q: got %d, %v, want %d", value, got, err, want)
		}
	}
	for _, value := range []string{"one", "0", "-1", "1.5"} {
		if got, err := doorFromEnv(func(string) string { return value }); err == nil {
			t.Errorf("AOCN_DOOR=%q: got %d, want an error", value, got)
		}
	}
}

func TestNotYet(t *testing.T) {
	closed := &api.ClosedError{Door: 3, OpensAt: time.Date(2026, 12, 3, 5, 0, 0, 0, time.UTC)}
	if got := notYet(closed, true).Error(); got != "not yet: door 3 opens Dec 3, 06:00 CET. See you then" {
		t.Errorf("signed in: got %q", got)
	}
	if got := notYet(closed, false).Error(); !strings.Contains(got, "aocn login, then run aocn open 3 again") {
		t.Errorf("signed out: got %q, want how a previewer gets the door", got)
	}
}

func TestOtherDoor(t *testing.T) {
	inCodespace := otherDoor(1, 3, true).Error()
	if !strings.Contains(inCodespace, "https://aocn.dev/doors/3") || strings.Contains(inCodespace, "--reset") {
		t.Errorf("in door 1's Codespace: got %q, want the door's page and no --reset", inCodespace)
	}
	elsewhere := otherDoor(1, 3, false).Error()
	if !strings.Contains(elsewhere, "https://aocn.dev/doors/3") || !strings.Contains(elsewhere, "aocn open 3 --reset") {
		t.Errorf("elsewhere: got %q, want the door's page and --reset", elsewhere)
	}
}
