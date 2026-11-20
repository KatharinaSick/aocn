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
// So one reader takes lines for all of them, and a question only gets what was typed after it
// was asked.
var stdin = newLines(os.Stdin)

// lines hands out what's typed, one line at a time, from a single reader.
type lines struct {
	in    io.Reader
	start sync.Once
	ch    chan string
}

func newLines(in io.Reader) *lines {
	return &lines{in: in, ch: make(chan string)}
}

// read waits for the next line typed from now on, or until ctx ends. A line typed before,
// that nobody waited for, is dropped. At the end of the input, it returns "" and io.EOF.
func (l *lines) read(ctx context.Context) (string, error) {
	l.start.Do(func() {
		go func() {
			r := bufio.NewReader(l.in)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					close(l.ch)
					return
				}
				l.ch <- line
			}
		}()
	})
	// Drop what was typed before the question.
drop:
	for {
		select {
		case _, ok := <-l.ch:
			if !ok {
				return "", io.EOF
			}
		default:
			break drop
		}
	}
	select {
	case line, ok := <-l.ch:
		if !ok {
			return "", io.EOF
		}
		return line, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
