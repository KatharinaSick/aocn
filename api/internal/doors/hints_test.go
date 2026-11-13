package doors

import (
	"slices"
	"testing"
)

func TestParseHints(t *testing.T) {
	hints, err := ParseHints([]byte("- first\n- |\n  second,\n  on two lines\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second,\non two lines\n"}; !slices.Equal(hints, want) {
		t.Errorf("got %q, want %q", hints, want)
	}
}

func TestParseHintsRefuses(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"no hints":      "[]",
		"not a list":    "hint: first",
		"not text":      "- {text: first}",
		"blank hint":    "- first\n- '  '",
		"two documents": "- first\n---\n- second",
	}
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			if hints, err := ParseHints([]byte(yaml)); err == nil {
				t.Errorf("got %q, want an error", hints)
			}
		})
	}
}
