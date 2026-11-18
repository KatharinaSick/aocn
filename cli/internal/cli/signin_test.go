package cli

import "testing"

func TestAgreed(t *testing.T) {
	for answer, want := range map[string]bool{
		"\n": true, "y\n": true, "Yes\n": true, "  Y  \n": true,
		"n\n": false, "N\n": false, "no\n": false, " nope\n": false,
	} {
		if got := agreed(answer); got != want {
			t.Errorf("agreed(%q) = %v, want %v", answer, got, want)
		}
	}
}

func TestCardURL(t *testing.T) {
	if got := cardURL("someone"); got != "https://aocn.dev/u/someone" {
		t.Errorf("got %q", got)
	}
}
