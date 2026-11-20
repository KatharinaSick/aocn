package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/katharinasick/aocn/cli/internal/auth"
)

// The sign-in screen. Signing in means leaving the terminal for github.com and coming back, so
// the code stands out in a box, Enter opens the page, and a spinner shows aocn is waiting. In
// a terminal only: piped or in a script, it's the plain line it always was.

// screen is how the sign-in screen may look where it's shown.
type screen struct {
	// styled: a terminal that shows colors, bold and links.
	styled bool
	// canOpen: aocn knows how to open a browser here, and someone can press Enter.
	canOpen bool
}

// screenHere finds out what the sign-in screen can do in this terminal.
func screenHere() screen {
	interactive := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
	return screen{
		styled:  interactive && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		canOpen: interactive && browserCommand(os.Getenv, exec.LookPath) != nil,
	}
}

// ANSI styles. The terminal's colors, never fixed ones, so it matches the station's palette.
const (
	bold   = "\x1b[1m"
	green  = "\x1b[32m"
	muted  = "\x1b[90m"
	under  = "\x1b[4m"
	reset  = "\x1b[0m"
	upLine = "\r\x1b[K"
)

// link makes text a link in terminals that know OSC 8; the others show the text.
func link(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// signInScreen is what the player reads before they go to github.com.
func signInScreen(code auth.DeviceCode, s screen) string {
	if !s.styled {
		return fmt.Sprintf("Sign in with GitHub: open %s and enter %s\n\nWaiting for GitHub…\n", code.VerificationURI, code.UserCode)
	}
	page := strings.TrimPrefix(code.VerificationURI, "https://")
	pad := strings.Repeat("─", utf8.RuneCountInString(code.UserCode)+4)
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %sSign in with GitHub%s\n\n", bold, reset)
	fmt.Fprintf(&b, "  1. Copy your code\n\n")
	fmt.Fprintf(&b, "       %s╭%s╮%s\n", muted, pad, reset)
	fmt.Fprintf(&b, "       %s│%s  %s%s%s%s  %s│%s\n", muted, reset, bold, green, code.UserCode, reset, muted, reset)
	fmt.Fprintf(&b, "       %s╰%s╯%s\n\n", muted, pad, reset)
	fmt.Fprintf(&b, "  2. Paste it on %s%s%s%s\n", under, green, link(code.VerificationURI, page), reset)
	if s.canOpen {
		fmt.Fprintf(&b, "     %sPress Enter to open it in your browser.%s\n", muted, reset)
	}
	// GitHub says 899 seconds for 15 minutes, so it's rounded up.
	if minutes := (code.ExpiresIn + 59) / 60; minutes > 0 {
		fmt.Fprintf(&b, "     %sThe code works for %d minutes.%s\n", muted, minutes, reset)
	}
	return b.String()
}

// browserCommand is how to open a page here, or nil if aocn doesn't know. VS Code and
// Codespaces set BROWSER to their own helper, which opens the page on the player's computer.
// getenv and lookPath are os.Getenv and exec.LookPath outside of tests.
func browserCommand(getenv func(string) string, lookPath func(string) (string, error)) []string {
	if b := strings.TrimSpace(getenv("BROWSER")); b != "" {
		return []string{b}
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{"open"}
	case "linux":
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return nil
		}
		if _, err := lookPath("xdg-open"); err == nil {
			return []string{"xdg-open"}
		}
	}
	return nil
}

// openBrowser opens url, and returns the line that says so.
func openBrowser(url string) string {
	command := browserCommand(os.Getenv, exec.LookPath)
	page := strings.TrimPrefix(url, "https://")
	if command == nil {
		return fmt.Sprintf("     %sOpen %s yourself.%s", muted, page, reset)
	}
	if err := exec.Command(command[0], append(command[1:], url)...).Start(); err != nil {
		return fmt.Sprintf("     %sCouldn't open a browser here. Open %s yourself.%s", muted, page, reset)
	}
	return fmt.Sprintf("     %sOpened %s.%s", muted, page, reset)
}

// spinner shows that aocn is waiting: a turning wheel and a text on the last line. What's
// written to it goes above the wheel, which keeps turning under it.
type spinner struct {
	out   io.Writer
	mu    sync.Mutex
	text  string
	frame int
	// pending is what was written since the last full line, held back until its line ends.
	pending []byte
	done    chan struct{}
	ended   chan struct{}
	over    bool
}

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// spin shows a spinner with text until stop is called, which clears it again.
func spin(out io.Writer, text string) *spinner {
	sp := &spinner{out: out, text: text, done: make(chan struct{}), ended: make(chan struct{})}
	go func() {
		defer close(sp.ended)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			sp.mu.Lock()
			sp.draw()
			sp.frame++
			sp.mu.Unlock()
			select {
			case <-sp.done:
				return
			case <-ticker.C:
			}
		}
	}()
	return sp
}

// draw shows the wheel and the text on the cursor's line. sp.mu is held.
func (sp *spinner) draw() {
	fmt.Fprintf(sp.out, "%s  %s%c%s %s", upLine, green, spinnerFrames[sp.frame%len(spinnerFrames)], reset, sp.text)
}

// set changes the spinner's text.
func (sp *spinner) set(text string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.text = text
	if !sp.over {
		sp.draw()
	}
}

// Write prints p's full lines above the spinner. A line that hasn't ended yet waits for the
// rest, so the wheel never lands in the middle of it. After stop, it writes straight through.
func (sp *spinner) Write(p []byte) (int, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.over {
		return sp.out.Write(p)
	}
	sp.pending = append(sp.pending, p...)
	i := bytes.LastIndexByte(sp.pending, '\n')
	if i < 0 {
		return len(p), nil
	}
	fmt.Fprint(sp.out, upLine)
	if _, err := sp.out.Write(sp.pending[:i+1]); err != nil {
		return 0, err
	}
	sp.pending = append(sp.pending[:0], sp.pending[i+1:]...)
	sp.draw()
	return len(p), nil
}

// note prints line above the spinner, which keeps turning. The player's Enter moved the
// cursor down a line, so the wheel left on the line above is cleared first.
func (sp *spinner) note(line string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.over {
		return
	}
	fmt.Fprintf(sp.out, "%s\x1b[1A%s%s\n", upLine, upLine, line)
}

// stop clears the spinner, and prints what was written to it without a line end yet. Notes
// after it are dropped.
func (sp *spinner) stop() {
	sp.mu.Lock()
	if sp.over {
		sp.mu.Unlock()
		return
	}
	sp.over = true
	sp.mu.Unlock()
	close(sp.done)
	<-sp.ended
	sp.mu.Lock()
	defer sp.mu.Unlock()
	fmt.Fprint(sp.out, upLine)
	sp.out.Write(sp.pending)
	sp.pending = nil
}
