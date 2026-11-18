package cli

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/katharinasick/aocn/cli/internal/auth"
)

func TestAgreed(t *testing.T) {
	for answer, want := range map[string]bool{
		"\n": true, "y\n": true, "Yes\n": true, "  Y  \n": true,
		"n\n": false, "N\n": false, "no\n": false, " nope\n": false,
	} {
		if got := agreed(answer); got != want {
			t.Errorf("agreed(%q) = %v, want %v", answer, got, want)
		}
	}
}

func TestCardURL(t *testing.T) {
	if got := cardURL("someone"); got != "https://aocn.dev/u/someone" {
		t.Errorf("got %q", got)
	}
}

func TestSignInScreen(t *testing.T) {
	code := auth.DeviceCode{UserCode: "WDJB-MJHT", VerificationURI: "https://github.com/login/device", ExpiresIn: 899}

	plain := signInScreen(code, screen{})
	if plain != "Sign in with GitHub: open https://github.com/login/device and enter WDJB-MJHT\n\nWaiting for GitHub…\n" {
		t.Errorf("plain: got %q", plain)
	}
	if strings.Contains(plain, "\x1b") {
		t.Error("plain: has escape codes")
	}

	styled := signInScreen(code, screen{styled: true, canOpen: true})
	for _, want := range []string{"WDJB-MJHT", "github.com/login/device", "Press Enter", "15 minutes", "╭─────────────╮"} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled: missing %q in %q", want, styled)
		}
	}
	if strings.Contains(signInScreen(code, screen{styled: true}), "Press Enter") {
		t.Error("styled without a browser: asks for Enter anyway")
	}
}

func TestBrowserCommand(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	noTools := func(string) (string, error) { return "", errors.New("not found") }
	if got := browserCommand(env(map[string]string{"BROWSER": "/vscode/helpers/browser.sh"}), noTools); len(got) != 1 || got[0] != "/vscode/helpers/browser.sh" {
		t.Errorf("BROWSER set: got %v", got)
	}
	if runtime.GOOS == "linux" {
		if got := browserCommand(env(nil), noTools); got != nil {
			t.Errorf("linux without a display: got %v, want none", got)
		}
	}
}
