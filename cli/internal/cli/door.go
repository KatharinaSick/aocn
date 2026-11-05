package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/katharinasick/aocn/cli/internal/check"
	"github.com/katharinasick/aocn/cli/internal/cluster"
	"github.com/katharinasick/aocn/cli/internal/door"
)

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in with GitHub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("login: not implemented yet")
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
				return fmt.Errorf("downloading doors isn't built yet, use --bundle")
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
		Long: `verify runs the open door's checks against the cluster and shows which pass. If you're
stuck, aocn hint gives you a hint. It works from any folder.`,
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

			results, err := os.CreateTemp("", "aocn-results-*.jsonl")
			if err != nil {
				return err
			}
			results.Close()
			defer os.Remove(results.Name())

			out := cmd.OutOrStdout()
			runErr := door.Verify(cmd.Context(), out, filepath.Join(dir, open.Folder), results.Name())
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			all, err := check.ReadResults(results.Name())
			if err != nil {
				return err
			}

			failed := 0
			for _, r := range all {
				if !r.Passed {
					failed++
				}
			}
			fmt.Fprintln(out)
			switch {
			case failed == 1:
				fmt.Fprintln(out, "Not there yet: 1 check failed. Stuck? Try aocn hint.")
			case failed > 1:
				fmt.Fprintf(out, "Not there yet: %d checks failed. Stuck? Try aocn hint.\n", failed)
			case runErr != nil:
				// The script stopped without a failed check, so it's broken, not the door.
				return fmt.Errorf("the door's checks stopped early: %w", runErr)
			case len(all) == 0:
				return fmt.Errorf("the door has no checks")
			default:
				fmt.Fprintln(out, "All checks passed.")
				return nil
			}
			return check.ErrFailed
		},
	}
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
