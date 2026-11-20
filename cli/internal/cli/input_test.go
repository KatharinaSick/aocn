package cli

import (
	"context"
	"errors"
	"io"
	"runtime"
	"testing"
)

// A question that stopped waiting doesn't take the answer to the next one, and a line typed
// while no question waits isn't anyone's answer.
func TestLinesOnlyAnswerTheQuestionAsked(t *testing.T) {
	r, w := io.Pipe()
	l := newLines(r)
	dropped := make(chan struct{})
	l.dropped = func() { dropped <- struct{}{} }

	// Enter to open the browser, but GitHub confirmed first: the question stops waiting.
	waiting, stop := context.WithCancel(context.Background())
	stop()
	if _, err := l.read(waiting); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}

	// Enter pressed late, before the next question: nobody waits, so it's dropped.
	go w.Write([]byte("\n"))
	<-dropped

	answer := make(chan string)
	go func() {
		line, _ := l.read(context.Background())
		answer <- line
	}()
	for !l.waiting() {
		runtime.Gosched()
	}
	go w.Write([]byte("n\n"))
	if got := <-answer; got != "n\n" {
		t.Errorf("got %q, want the answer typed after the question", got)
	}

	w.Close()
	if _, err := l.read(context.Background()); err != io.EOF {
		t.Errorf("at the end: %v, want EOF", err)
	}
}

// Input piped in answers the first question, however fast it arrives.
func TestLinesAnswerFromAPipe(t *testing.T) {
	r, w := io.Pipe()
	l := newLines(r)
	go func() {
		w.Write([]byte("y\n"))
		w.Close()
	}()
	if got, err := l.read(context.Background()); got != "y\n" || err != nil {
		t.Errorf("got %q, %v", got, err)
	}
}
