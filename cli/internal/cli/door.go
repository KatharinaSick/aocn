package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/katharinasick/aocn/cli/internal/api"
	"github.com/katharinasick/aocn/cli/internal/auth"
	"github.com/katharinasick/aocn/cli/internal/check"
	"github.com/katharinasick/aocn/cli/internal/cluster"
	"github.com/katharinasick/aocn/cli/internal/door"
)

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in with GitHub",
		Long: `login signs you in with your GitHub account, so aocn verify can save your progress under
it. You confirm a code on github.com; aocn gets no access to your repositories or anything
private, only your public profile.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			code, err := auth.StartDeviceFlow(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Open %s and enter this code: %s\n\nWaiting for GitHub…\n", code.VerificationURI, code.UserCode)
			token, err := auth.WaitForToken(cmd.Context(), code)
			if err != nil {
				return err
			}
			if err := auth.SaveToken(token); err != nil {
				return fmt.Errorf("saving your sign-in: %w", err)
			}
			login, err := auth.Login(cmd.Context(), token)
			if err != nil {
				// Signed in all the same; only the name is missing.
				fmt.Fprintln(out, "Signed in.")
				return nil
			}
			fmt.Fprintf(out, "Signed in as %s.\n", login)
			return nil
		},
	}
}

func newOpenCmd() *cobra.Command {
	var bundle string
	var reset bool
	cmd := &cobra.Command{
		Use:   "open DOOR",
		Short: "Start a door on a fresh cluster",
		Long: `open puts the door's files in doors/ and starts the door on a fresh cluster. Only one
door runs at a time, so whatever ran in the cluster before is gone.

Your files are never deleted. If you opened the door before, you continue with your files,
and only the door's own files in .aocn/ are replaced with the newest version. --reset starts
over with the original files, and moves yours aside.`,
		Example: `  aocn open 1
  aocn open 1 --reset
  aocn open 1 --bundle ./bundle.tar.gz`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := strconv.Atoi(args[0])
			if err != nil || number < 1 {
				return fmt.Errorf("door must be a number like 1, got %q", args[0])
			}
			if bundle == "" {
				downloaded, err := download(cmd, number)
				if err != nil {
					return err
				}
				defer os.Remove(downloaded)
				bundle = downloaded
			}
			folder, err := door.BundleFolder(bundle, number)
			if err != nil {
				return err
			}
			dir, err := door.Dir()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if door.Exists(dir, folder) {
				if !reset {
					reset = askReset(cmd, folder)
				}
				if reset {
					aside, err := door.MoveAside(dir, folder)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "Moved your files to doors/%s/.\n", aside)
				}
			}
			if door.Exists(dir, folder) {
				// The player's files stay, but the door's own files come from the newest bundle.
				if err := door.Refresh(bundle, dir, folder); err != nil {
					return err
				}
			} else if err := door.Extract(bundle, dir); err != nil {
				return err
			}

			if err := cluster.Recreate(cmd.Context(), out, door.KindConfig(filepath.Join(dir, folder))); err != nil {
				return err
			}
			// Saved before init, so a restart picks the door up even if init failed.
			if err := door.SaveOpen(dir, door.Open{Number: number, Folder: folder}); err != nil {
				return err
			}
			if err := door.Init(cmd.Context(), out, filepath.Join(dir, folder)); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nDoor %d is open. Its files are in doors/%s/.\n", number, folder)
			return nil
		},
	}
	cmd.Flags().StringVar(&bundle, "bundle", "", "use a local bundle instead of downloading it")
	cmd.Flags().BoolVar(&reset, "reset", false, "start over with the original files, and move yours aside")
	return cmd
}

// download fetches the door's bundle into a temporary file and returns its path. Before the
// door opens, it says when to come back.
func download(cmd *cobra.Command, number int) (string, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("aocn-door-%02d-*.tar.gz", number))
	if err != nil {
		return "", err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Downloading door %d…\n", number)
	preview, err := api.DownloadBundle(cmd.Context(), number, f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(f.Name())
		var closed *api.ClosedError
		switch {
		case errors.As(err, &closed):
			return "", fmt.Errorf("not yet: %w. See you then", closed)
		case errors.Is(err, api.ErrNoDoor):
			return "", fmt.Errorf("there is no door %d", number)
		}
		return "", err
	}
	if !preview.OpensAt.IsZero() {
		fmt.Fprintf(cmd.OutOrStdout(), "Preview: %s. You get it early because you're a previewer.\n",
			&api.ClosedError{Door: number, OpensAt: preview.OpensAt})
	}
	return f.Name(), nil
}

// askReset warns that the door was opened before and asks whether to start over. Without a
// terminal to ask in, it keeps the player's files.
func askReset(cmd *cobra.Command, folder string) bool {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "You opened this door before. Your files are in doors/%s/.\n", folder)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(out, "Continuing with your files. To start over, run it again with --reset.")
		return false
	}
	fmt.Fprint(out, "Continue with your files? Start over with n, your files are moved aside. [Y/n] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "n")
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Start the open door again after a container restart",
		Long: `resume starts the cluster again and re-runs the open door's init, without resetting
anything. The devcontainer runs it on every start.`,
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := door.Dir()
			if err != nil {
				return err
			}
			open, ok, err := door.LoadOpen(dir)
			if err != nil || !ok {
				return err
			}
			out := cmd.OutOrStdout()
			folder := filepath.Join(dir, open.Folder)
			if err := cluster.Start(cmd.Context(), out, door.KindConfig(folder)); err != nil {
				return err
			}
			return door.Init(cmd.Context(), out, folder)
		},
	}
}

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check the open door",
		Long: `verify runs the open door's checks against the cluster and shows which pass. Once you're
signed in (aocn login), it saves the result under your GitHub account. If you're stuck, aocn
hint gives you a hint. It works from any folder.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := door.Dir()
			if err != nil {
				return err
			}
			open, ok, err := door.LoadOpen(dir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no door is open yet. Start one with: aocn open 1")
			}

			// Always from the API, also for a door opened from a local bundle: nothing in the
			// player's folder decides whether a door passes.
			checks, err := api.Checks(cmd.Context(), open.Number)
			if err != nil {
				var closed *api.ClosedError
				if errors.As(err, &closed) {
					return fmt.Errorf("not yet: %w", closed)
				}
				return err
			}
			if len(checks) == 0 {
				return fmt.Errorf("door %d has no checks", open.Number)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Checking door %d. This takes about a minute.\n", open.Number)
			outcomes, err := check.Run(cmd.Context(), out, checks)
			if err != nil {
				return err
			}

			failed := check.Failed(outcomes)
			fmt.Fprintln(out)
			switch failed {
			case 0:
				fmt.Fprintln(out, "All checks passed.")
			case 1:
				fmt.Fprintln(out, "Not there yet: 1 check failed. Stuck? Try aocn hint.")
			default:
				fmt.Fprintf(out, "Not there yet: %d checks failed. Stuck? Try aocn hint.\n", failed)
			}

			save(cmd, open.Number, outcomes)
			if failed > 0 {
				return check.ErrFailed
			}
			return nil
		},
	}
}

// save sends a run to the API, so it counts for the player's GitHub account. Whether it
// worked never changes what verify found, so a problem here is a note, not an error.
func save(cmd *cobra.Command, number int, outcomes []check.Outcome) {
	out := cmd.OutOrStdout()
	token, err := auth.Token()
	if err == nil && token == "" {
		fmt.Fprintln(out, "Not saved: sign in with aocn login to keep your progress.")
		return
	}
	if err == nil {
		var saved api.Saved
		if saved, err = api.SendResults(cmd.Context(), number, outcomes); err == nil {
			fmt.Fprintf(out, "Saved for %s.\n", saved.Login)
			return
		}
	}
	if cmd.Context().Err() != nil {
		return
	}
	fmt.Fprintf(out, "Not saved: %v\n", err)
}

func newHintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hint",
		Short: "Show the open door's next hint",
		Long: `hint shows the open door's next hint. Each one gives away a little more, and asking
again after the last one shows the last one again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := door.Dir()
			if err != nil {
				return err
			}
			open, ok, err := door.LoadOpen(dir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no door is open yet. Start one with: aocn open 1")
			}
			hints, err := door.Hints(filepath.Join(dir, open.Folder))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(hints) == 0 {
				fmt.Fprintln(out, "This door has no hints.")
				return nil
			}

			i, err := door.NextHint(dir, open.Number, len(hints))
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "💡 Hint %d of %d\n\n%s\n", i+1, len(hints), strings.TrimSpace(hints[i]))
			if i == len(hints)-1 {
				fmt.Fprintln(out, "\nThat's the last hint.")
			}
			return nil
		},
	}
}

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the aocn version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if version == "" {
				version = "dev"
			}
			cmd.Println(version)
		},
	}
}
