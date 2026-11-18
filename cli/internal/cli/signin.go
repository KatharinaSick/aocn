package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/katharinasick/aocn/cli/internal/api"
	"github.com/katharinasick/aocn/cli/internal/auth"
)

// accountNotice is what signing in means for someone without an aocn.dev account yet. GitHub's
// authorize screen only says what aocn may read, so this is where the player agrees.
const accountNotice = `You don't have an aocn.dev account yet. Signing in creates one under your GitHub account,
so aocn verify can save your progress: your GitHub login, user ID and avatar, and your
results. Your profile is private unless you make it public on aocn.dev.
Privacy policy: https://aocn.dev/privacy`

// errNotSignedIn ends aocn verify for a player who didn't want an account.
var errNotSignedIn = errors.New("not signed in, so nothing was checked. aocn verify needs an account to save your progress")

// ensureSignedIn signs the player in unless they already are. aocn verify calls it first, so
// every run is saved and nobody has to remember aocn login. It asks before creating an account.
func ensureSignedIn(cmd *cobra.Command) error {
	token, err := auth.Token()
	if err == nil && token != "" {
		return nil
	}
	return signIn(cmd, true)
}

// signIn signs the player in with GitHub, with a code to confirm on github.com. The GitHub
// token goes to the API, and aocn keeps the API's. A Codespace's own GITHUB_TOKEN can't be
// used: the API only takes tokens GitHub issued to aocn.dev's OAuth app. Someone who has an
// account is signed in right away; for anyone else it explains the account first, and with
// ask, asks.
func signIn(cmd *cobra.Command, ask bool) error {
	out := cmd.OutOrStdout()
	code, err := auth.StartDeviceFlow(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Sign in with GitHub: open %s and enter %s\n\nWaiting for GitHub…\n", code.VerificationURI, code.UserCode)
	githubToken, err := auth.WaitForToken(cmd.Context(), code)
	if err != nil {
		return err
	}
	session, err := api.SignIn(cmd.Context(), githubToken, false)
	if errors.Is(err, api.ErrNoAccount) {
		return createAccount(cmd, githubToken, ask)
	}
	if err != nil {
		return err
	}
	return keep(cmd, session)
}

// createAccount explains the account, asks if ask is set, and creates it.
func createAccount(cmd *cobra.Command, githubToken string, ask bool) error {
	out := cmd.OutOrStdout()
	if ask && !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("you don't have an aocn.dev account yet. Run aocn login to create one, then aocn verify again")
	}
	fmt.Fprintf(out, "\n%s\n\n", accountNotice)
	if ask {
		fmt.Fprint(out, "Create your account and continue? [Y/n] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !agreed(answer) {
			return errNotSignedIn
		}
	}
	session, err := api.SignIn(cmd.Context(), githubToken, true)
	if err != nil {
		return err
	}
	return keep(cmd, session)
}

// agreed reads the answer to a [Y/n] question: yes unless it starts with n.
func agreed(answer string) bool {
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "n")
}

func keep(cmd *cobra.Command, session api.Session) error {
	if err := auth.SaveToken(session.Token); err != nil {
		return fmt.Errorf("saving your sign-in: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s.\n", session.Login)
	return nil
}

// cardURL is the player's profile card on the website.
func cardURL(login string) string {
	return "https://aocn.dev/u/" + login
}
