package cli

import (
	"bytes"
	"testing"
)

// Every line of a tool's output is indented, however its writes split the lines.
func TestIndented(t *testing.T) {
	var out bytes.Buffer
	w := &indented{out: &out, start: "> ", end: "<"}
	for _, part := range []string{"one\ntw", "o\n", "", "three\n\nfour"} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("wrote %d of %q: %v", n, part, err)
		}
	}
	want := "> one<\n> two<\n> three<\n> <\n> four"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestStepsCount(t *testing.T) {
	var out bytes.Buffer
	s := &steps{out: &out, total: 2}
	s.next("First").Write([]byte("tool output\n"))
	s.next("Second")
	s.done("Done.")
	want := "\n[1/2] First\n    tool output\n\n[2/2] Second\n\nDone.\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}
