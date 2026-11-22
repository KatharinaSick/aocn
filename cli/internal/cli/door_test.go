package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestScoreLine(t *testing.T) {
	tests := []struct {
		score api.Score
		want  string
	}{
		{api.Score{Points: 100}, "100 points."},
		{api.Score{Points: 95, Hints: 1, FailedVerifies: 1}, "95 points (1 hint), 1 failed verify."},
		{api.Score{Points: 80, Hints: 2, FailedVerifies: 3}, "80 points (2 hints), 3 failed verifies."},
		{api.Score{Points: 100, FailedVerifies: 2}, "100 points, 2 failed verifies."},
		{api.Score{Points: 100, Preview: true}, "100 points, once it opens."},
	}
	for _, tt := range tests {
		if got := scoreLine(tt.score); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestHintCostLine(t *testing.T) {
	next := &api.NextHint{Hint: 3, Cost: 30}
	tests := []struct {
		cost  int
		state api.HintState
		want  string
	}{
		{15, api.HintState{Next: next}, "That cost 15 points. Stuck again? aocn hint opens the next one."},
		{30, api.HintState{}, "That cost 30 points. That was the last hint."},
		{0, api.HintState{Next: next, Fixed: true}, "Free: you fixed this door already. Stuck again? aocn hint opens the next one."},
	}
	for _, tt := range tests {
		if got := hintCostLine(tt.cost, tt.state); got != tt.want {
			t.Errorf("%d, %+v: got %q, want %q", tt.cost, tt.state, got, tt.want)
		}
	}
}

// A hint is POLARIS-7's line: her name on the first line, the others under it.
func TestPrintHint(t *testing.T) {
	var out bytes.Buffer
	printHint(&out, false, 2, 3, "\nFirst line.\n  a command\nLast line.\n")
	want := "Hint 2 of 3\nPOLARIS-7 > First line.\n              a command\n            Last line.\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestFixedBanner(t *testing.T) {
	plainText := fixedBanner(12, false)
	if plainText != "  Door 12 fixed! Every goal is met.\n" {
		t.Errorf("plain: got %q", plainText)
	}
	// Every line of the frame is as wide as the others, with the party popper two columns.
	styled := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(fixedBanner(12, true), "")
	lines := strings.Split(strings.TrimSuffix(styled, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), styled)
	}
	width := func(s string) int { return utf8.RuneCountInString(strings.ReplaceAll(s, "🎉", "🎉🎉")) }
	for _, l := range lines[1:] {
		if width(l) != width(lines[0]) {
			t.Errorf("line %q is %d wide, the top %d", l, width(l), width(lines[0]))
		}
	}
}

func TestNotThereYet(t *testing.T) {
	if got, want := notThereYet(1, 3, false), "  Not there yet: 1 of 3 goals met.\n  Stuck? Try aocn hint.\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
