package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// A question that stopped waiting doesn't take the answer to the next one, and a line typed
// before a question isn't its answer.
func TestLinesOnlyAnswerTheQuestionAsked(t *testing.T) {
	r, w := io.Pipe()
	l := newLines(r)

	// Enter to open the browser, but GitHub confirmed first: the question stops waiting.
	waiting, stop := context.WithCancel(context.Background())
	stop()
	if _, err := l.read(waiting); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}

	// Enter pressed late, before the next question.
	go w.Write([]byte("\n"))
	time.Sleep(50 * time.Millisecond)

	answer := make(chan string)
	go func() {
		line, _ := l.read(context.Background())
		answer <- line
	}()
	time.Sleep(50 * time.Millisecond)
	go w.Write([]byte("n\n"))
	select {
	case got := <-answer:
		if got != "n\n" {
			t.Errorf("got %q, want the answer typed after the question", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no answer")
	}

	w.Close()
	if _, err := l.read(context.Background()); err != io.EOF {
		t.Errorf("at the end: %v, want EOF", err)
	}
}
