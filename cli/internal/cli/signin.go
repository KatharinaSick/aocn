package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/katharinasick/aocn/cli/internal/api"
	"github.com/katharinasick/aocn/cli/internal/auth"
)

// ensureSignedIn signs the player in unless they already are. aocn verify calls it first, so
// every run is saved and nobody has to remember aocn login.
func ensureSignedIn(cmd *cobra.Command) error {
	token, err := auth.Token()
	if err == nil && token != "" {
		return nil
	}
	return signIn(cmd)
}

// signInInCodespace signs the player in with the Codespace's GitHub token, if they aren't
// signed in yet and run in a Codespace. There's nothing to confirm, so aocn open does it before
// downloading: a previewer gets their early door, and aocn verify has nothing left to ask.
// It never fails a command; if it doesn't work, aocn verify asks later.
func signInInCodespace(cmd *cobra.Command) {
	if token, err := auth.Token(); err != nil || token != "" {
		return
	}
	githubToken := auth.CodespaceToken(os.Getenv)
	if githubToken == "" {
		return
	}
	if session, err := api.SignIn(cmd.Context(), githubToken); err == nil {
		_ = keep(cmd, session)
	}
}

// signIn signs the player in with GitHub: in a Codespace with the Codespace's own token,
// with nothing to confirm, and otherwise, or if that fails, with a code to confirm on
// github.com. Either way the GitHub token goes to the API once, and aocn keeps the API's.
func signIn(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	if githubToken := auth.CodespaceToken(os.Getenv); githubToken != "" {
		session, err := api.SignIn(cmd.Context(), githubToken)
		if err == nil {
			return keep(cmd, session)
		}
		if cmd.Context().Err() != nil {
			return err
		}
		fmt.Fprintf(out, "Signing in with your Codespace didn't work (%v), so let's do it on github.com.\n", err)
	}

	code, err := auth.StartDeviceFlow(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Sign in with GitHub to save your progress: open %s and enter %s\n\nWaiting for GitHub…\n",
		code.VerificationURI, code.UserCode)
	githubToken, err := auth.WaitForToken(cmd.Context(), code)
	if err != nil {
		return err
	}
	session, err := api.SignIn(cmd.Context(), githubToken)
	if err != nil {
		return err
	}
	return keep(cmd, session)
}

func keep(cmd *cobra.Command, session api.Session) error {
	if err := auth.SaveToken(session.Token); err != nil {
		return fmt.Errorf("saving your sign-in: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s.\n", session.Login)
	return nil
}
