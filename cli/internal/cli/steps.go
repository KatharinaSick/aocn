package cli

import (
	"bytes"
	"fmt"
	"io"
)

// steps is what aocn open and aocn resume say while they work. Each step gets a header that
// stands out, and what the tools print underneath is indented and quiet, so the player sees
// where it is without reading kind's or the door's own output.
type steps struct {
	out    io.Writer
	styled bool
	total  int
	n      int
}

// next starts the next step and returns where its tools write their output.
func (s *steps) next(title string) io.Writer {
	s.n++
	if s.styled {
		fmt.Fprintf(s.out, "\n%s%s▸ %s%s %s%d/%d%s\n", bold, green, title, reset, muted, s.n, s.total, reset)
		return &indented{out: s.out, start: "    " + muted, end: reset}
	}
	fmt.Fprintf(s.out, "\n[%d/%d] %s\n", s.n, s.total, title)
	return &indented{out: s.out, start: "    "}
}

// done says that everything worked, and what's next.
func (s *steps) done(line string) {
	if s.styled {
		fmt.Fprintf(s.out, "\n%s%s✓ %s%s\n", bold, green, line, reset)
		return
	}
	fmt.Fprintf(s.out, "\n%s\n", line)
}

// indented writes every line with start before it and end after it.
type indented struct {
	out        io.Writer
	start, end string
	// mid means the last write ended inside a line.
	mid bool
}

func (w *indented) Write(p []byte) (int, error) {
	n := len(p)
	var b bytes.Buffer
	for len(p) > 0 {
		if !w.mid {
			b.WriteString(w.start)
			w.mid = true
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			b.Write(p)
			break
		}
		b.Write(p[:i])
		b.WriteString(w.end)
		b.WriteByte('\n')
		w.mid = false
		p = p[i+1:]
	}
	if _, err := w.out.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return n, nil
}
