package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"
)

// stdin is every answer aocn reads from the player. Reading a line can't be cancelled, so a
// question that stops waiting, like "press Enter to open the browser" once GitHub confirmed
// the code by itself, would leave a read behind that takes the answer to the next question.
// So one reader takes lines for all of them, and a question only gets what was typed while it
// was asked.
var stdin = newLines(os.Stdin)

// lines hands out what's typed, one line at a time, from a single reader. A line typed while
// no question waits is dropped.
type lines struct {
	in    io.Reader
	start sync.Once

	mu     sync.Mutex
	waiter chan string // the question waiting now, or nil
	eof    bool
	// dropped is called after a line was dropped, for tests. Nil otherwise.
	dropped func()
}

func newLines(in io.Reader) *lines {
	return &lines{in: in}
}

// read waits for the next line typed from now on, or until ctx ends. At the end of the input,
// it returns "" and io.EOF.
func (l *lines) read(ctx context.Context) (string, error) {
	// Buffered, so the reader never blocks on a question that stopped waiting.
	ch := make(chan string, 1)
	l.mu.Lock()
	if l.eof {
		l.mu.Unlock()
		return "", io.EOF
	}
	l.waiter = ch
	l.mu.Unlock()
	// Started once the first question waits, so input piped in answers it.
	l.start.Do(func() { go l.readAll() })

	select {
	case line, ok := <-ch:
		if !ok {
			return "", io.EOF
		}
		return line, nil
	case <-ctx.Done():
		l.mu.Lock()
		if l.waiter == ch {
			l.waiter = nil
		}
		l.mu.Unlock()
		return "", ctx.Err()
	}
}

// waiting reports whether a question waits for a line right now.
func (l *lines) waiting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waiter != nil
}

// readAll hands every line to the question waiting for it, until the input ends.
func (l *lines) readAll() {
	r := bufio.NewReader(l.in)
	for {
		line, err := r.ReadString('\n')
		l.mu.Lock()
		if err != nil {
			l.eof = true
			if l.waiter != nil {
				close(l.waiter)
				l.waiter = nil
			}
			l.mu.Unlock()
			return
		}
		waiter, dropped := l.waiter, l.dropped
		l.waiter = nil
		l.mu.Unlock()
		if waiter != nil {
			waiter <- line
		} else if dropped != nil {
			dropped()
		}
	}
}
