package cli

import (
	"bytes"
	"strings"
	"sync"
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

// What's written to a spinner shows above it, one full line at a time, and what's left of an
// unfinished line comes out when it stops.
func TestSpinnerWritesAbove(t *testing.T) {
	var out syncBuffer
	sp := spin(&out, "waiting")
	sp.Write([]byte("  ✅ ready"))
	sp.Write([]byte("\nhalf"))
	sp.set("still waiting")
	sp.stop()
	got := out.String()
	if !strings.Contains(got, upLine+"  ✅ ready\n") || !strings.HasSuffix(got, upLine+"half") || !strings.Contains(got, "still waiting") {
		t.Errorf("got %q", got)
	}
}

// syncBuffer is a bytes.Buffer the spinner's goroutine and the test can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
