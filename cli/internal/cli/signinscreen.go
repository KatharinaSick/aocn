package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
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

// waitForEnter waits until the player presses Enter, or ctx ends.
func waitForEnter(ctx context.Context, in io.Reader) error {
	pressed := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(in).ReadString('\n')
		close(pressed)
	}()
	select {
	case <-pressed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

// openBrowser opens url, and says so.
func openBrowser(out io.Writer, url string) {
	command := browserCommand(os.Getenv, exec.LookPath)
	page := strings.TrimPrefix(url, "https://")
	if command == nil {
		return
	}
	if err := exec.Command(command[0], append(command[1:], url)...).Start(); err != nil {
		fmt.Fprintf(out, "     %sCouldn't open a browser here. Open %s yourself.%s\n", muted, page, reset)
		return
	}
	fmt.Fprintf(out, "     %sOpened %s.%s\n", muted, page, reset)
}

// spin shows a spinner with text until the returned stop is called, which clears it again.
func spin(out io.Writer, text string) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(out, "%s  %s%c%s %s", upLine, green, frames[i%len(frames)], reset, text)
			select {
			case <-done:
				fmt.Fprint(out, upLine)
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}
