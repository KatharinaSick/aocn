package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
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
		Long: `login signs you in with your GitHub account, so aocn verify saves your progress under it.
aocn verify asks by itself the first time, so you only need this to switch accounts, or to
open a door early as a previewer.

You confirm a code on github.com. aocn gets no access to your repositories or anything private,
only your public profile.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Typing aocn login already says yes, so it explains a new account without asking.
			return signIn(cmd, false)
		},
	}
}

func newOpenCmd() *cobra.Command {
	var bundle string
	var reset bool
	cmd := &cobra.Command{
		Use:   "open DOOR",
		Short: "Start a door on a fresh cluster",
		Long: `open puts the door's files in your station and starts the door on a fresh cluster. Your
station is the folder you play in (play/ in the devcontainer, or the current folder), and it
holds one door: every door has its own Codespace.

Your files are never deleted. If you opened the door before, you continue with your files,
and only aocn's own files (.aocn/ and README.md) are replaced with the newest version.
--reset starts over with the original files, and moves yours to .old/.`,
		Example: `  aocn open 1
  aocn open 1 --reset
  aocn open 1 --bundle ./bundle.tar.gz`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := strconv.Atoi(args[0])
			if err != nil || number < 1 {
				return fmt.Errorf("door must be a number like 1, got %q", args[0])
			}
			return openDoor(cmd, number, bundle, reset)
		},
	}
	cmd.Flags().StringVar(&bundle, "bundle", "", "use a local bundle instead of downloading it")
	cmd.Flags().BoolVar(&reset, "reset", false, "start over with the original files, and move yours to .old/")
	return cmd
}

// openDoor puts the door's files in place and starts it on a fresh cluster, from bundle or,
// without one, from a download. aocn open runs it, and aocn resume the first time a door's own
// Codespace starts.
func openDoor(cmd *cobra.Command, number int, bundle string, reset bool) error {
	station, err := door.Station()
	if err != nil {
		return err
	}
	before, opened, err := door.LoadOpen(station)
	if err != nil {
		return err
	}
	// A station holds one door. Another one is refused before anything is downloaded, so
	// nobody loses a door by typing the wrong number; outside a door's own Codespace, --reset
	// replaces it.
	if opened && before.Number != number {
		if !reset || os.Getenv("AOCN_DOOR") != "" {
			return otherDoor(before.Number, number, os.Getenv("AOCN_DOOR") != "")
		}
	}

	total := 2
	if bundle == "" {
		total = 3
	}
	st := &steps{out: cmd.OutOrStdout(), styled: screenHere().styled, total: total}
	if bundle == "" {
		downloaded, err := download(cmd, st.next(fmt.Sprintf("Downloading door %d", number)), number)
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
	out := cmd.OutOrStdout()
	if opened && before.Number == number && !reset {
		reset = askReset(cmd)
	}
	continuing := opened && before.Number == number && !reset
	if opened && !continuing {
		aside, err := door.MoveAside(station, before.Folder)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Moved door %d's files to %s/.\n", before.Number, aside)
	}

	if continuing {
		// The player's files stay, but aocn's come from the newest bundle.
		if err := door.Refresh(bundle, station); err != nil {
			return err
		}
	} else {
		strangers, err := door.Strangers(bundle, station)
		if err != nil {
			return err
		}
		if len(strangers) > 0 {
			return fmt.Errorf("aocn open puts the door's files right in this folder, but it has other things in it: %s. Run it in an empty folder", strings.Join(strangers, ", "))
		}
		if err := door.Extract(bundle, station); err != nil {
			return err
		}
	}

	if err := cluster.Recreate(cmd.Context(), st.next("Starting the station's cluster"), door.KindConfig(station)); err != nil {
		return err
	}
	// Saved before init, so a restart picks the door up even if init failed.
	if err := door.SaveOpen(station, door.Open{Number: number, Folder: folder}); err != nil {
		return err
	}
	if err := door.Init(cmd.Context(), st.next(fmt.Sprintf("Setting up door %d", number)), station); err != nil {
		return err
	}
	st.done(fmt.Sprintf("Door %d is open. Start with README.md: it has your mission.", number))
	return nil
}

// otherDoor is the answer to opening a door in a station that holds another one. In a door's
// own Codespace, it points to the other door's page, which opens its own Codespace.
func otherDoor(open, asked int, doorsCodespace bool) error {
	page := fmt.Sprintf("https://aocn.dev/doors/%d", asked)
	if doorsCodespace {
		return fmt.Errorf("this Codespace is door %d's. Door %d has its own: open it from %s", open, asked, page)
	}
	return fmt.Errorf("door %d is open here, and a station holds one door. Play door %d in its own Codespace, from %s, or replace door %d here with: aocn open %d --reset (its files are moved to .old/)", open, asked, page, open, asked)
}

// download fetches the door's bundle into a temporary file and returns its path, and says
// what it does on out. Before the door opens, it says when to come back.
func download(cmd *cobra.Command, out io.Writer, number int) (string, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("aocn-door-%02d-*.tar.gz", number))
	if err != nil {
		return "", err
	}
	preview, err := api.DownloadBundle(cmd.Context(), number, f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(f.Name())
		var closed *api.ClosedError
		switch {
		case errors.As(err, &closed):
			token, tokenErr := auth.Token()
			return "", notYet(closed, tokenErr == nil && token != "")
		case errors.Is(err, api.ErrNoDoor):
			return "", fmt.Errorf("there is no door %d", number)
		}
		return "", err
	}
	if !preview.OpensAt.IsZero() {
		fmt.Fprintf(out, "Preview: %s. You get it early because you're a previewer.\n",
			&api.ClosedError{Door: number, OpensAt: preview.OpensAt})
	}
	return f.Name(), nil
}

// notYet is the answer for a door that isn't open yet. Signed out, a previewer gets the same
// answer from the API as everyone else, so it says how to get the door early.
func notYet(closed *api.ClosedError, signedIn bool) error {
	if signedIn {
		return fmt.Errorf("not yet: %w. See you then", closed)
	}
	return fmt.Errorf("not yet: %w. Previewing? Sign in with aocn login, then run aocn open %d again", closed, closed.Door)
}

// askReset warns that the door was opened before and asks whether to start over. Without a
// terminal to ask in, it keeps the player's files.
func askReset(cmd *cobra.Command) bool {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "You opened this door before, and your files are still here.")
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(out, "Continuing with your files. To start over, run it again with --reset.")
		return false
	}
	fmt.Fprint(out, "Continue with your files? Start over with n, your files are moved aside. [Y/n] ")
	answer, _ := stdin.read(cmd.Context())
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "n")
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Start the open door again after a container restart",
		Long: `resume starts the cluster again and re-runs the open door's init, without resetting
anything. The devcontainer runs it on every start.

With no door open yet, it opens the door in AOCN_DOOR, which a door's own Codespace sets. So
that Codespace opens its door on its first start, and tries again on every start until the
door is open.`,
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			station, err := door.Station()
			if err != nil {
				return err
			}
			_, ok, err := door.LoadOpen(station)
			if err != nil {
				return err
			}
			if !ok {
				number, err := doorFromEnv(os.Getenv)
				if err != nil || number == 0 {
					return err
				}
				return openDoor(cmd, number, "", false)
			}
			st := &steps{out: cmd.OutOrStdout(), styled: screenHere().styled, total: 2}
			if err := cluster.Start(cmd.Context(), st.next("Starting the station's cluster again"), door.KindConfig(station)); err != nil {
				return err
			}
			if err := door.Init(cmd.Context(), st.next("Setting up the door again"), station); err != nil {
				return err
			}
			st.done("The station is back. Your files are as you left them.")
			return nil
		},
	}
}

// doorFromEnv reads AOCN_DOOR, the door a door's own Codespace opens by itself. 0 means none
// is set. getenv is os.Getenv outside of tests.
func doorFromEnv(getenv func(string) string) (int, error) {
	text := strings.TrimSpace(getenv("AOCN_DOOR"))
	if text == "" {
		return 0, nil
	}
	number, err := strconv.Atoi(text)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("AOCN_DOOR must be a door number like 1, got %q", text)
	}
	return number, nil
}

func newWhereCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "where",
		Short: "Print the open door's folder",
		Long: `where prints the station, the folder the open door's files are in, and nothing if no
door is open. The devcontainer's shell uses it to change into the door after aocn open and in
every new terminal.`,
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			station, err := door.Station()
			if err != nil {
				return err
			}
			ok, err := door.IsOpen(station)
			if err != nil || !ok {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), station)
			return nil
		},
	}
}

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check the open door",
		Long: `verify runs the open door's checks against the cluster, shows which pass and saves the
result under your GitHub account. The first time, it signs you in. If you're stuck, aocn hint
gives you a hint. It works from any folder.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			station, err := door.Station()
			if err != nil {
				return err
			}
			open, ok, err := door.LoadOpen(station)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no door is open yet. Start one with: aocn open 1")
			}
			// Before the checks run, so every run is saved.
			if err := ensureSignedIn(cmd); err != nil {
				return err
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
			fmt.Fprintf(out, "Checking door %d. This takes a moment.\n", open.Number)
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
	saved, err := api.SendResults(cmd.Context(), number, outcomes)
	if err == nil && saved.Passed {
		if saved.Score != nil {
			fmt.Fprintln(out, scoreLine(number, *saved.Score))
		}
		fmt.Fprintf(out, "Saved for %s. Your card: %s\n", saved.Login, cardURL(saved.Login))
		// Every door has its own Codespace, and each one keeps using the player's free storage
		// until it's deleted.
		if os.Getenv("CODESPACES") == "true" {
			fmt.Fprintln(out, "Done with this door? Delete this Codespace at https://github.com/codespaces, so it doesn't use up your free storage.")
		}
		return
	}
	if err == nil {
		fmt.Fprintf(out, "Saved for %s.\n", saved.Login)
		return
	}
	if cmd.Context().Err() != nil {
		return
	}
	fmt.Fprintf(out, "Not saved: %v\n", err)
}

func newHintCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "hint [N]",
		Short: "Open the open door's next hint",
		Long: `hint opens the open door's next hint. Each one gives away a little more than the last, and
costs a little more: the first 5 points, the second 15, the third 30, of the 100 a door is
worth. It asks before anything costs points. Hints come in order.

Hints you opened are free to read again: aocn hint N shows hint N, and once all are open,
aocn hint shows them all. Once you fixed the door, every hint is free.

The hints you opened count for your GitHub account, in every Codespace, so the first time,
it signs you in.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			want := 0
			if len(args) == 1 {
				n, err := strconv.Atoi(args[0])
				if err != nil || n < 1 {
					return fmt.Errorf("%q isn't a hint number. Hints are 1, 2, 3, …", args[0])
				}
				want = n
			}
			station, err := door.Station()
			if err != nil {
				return err
			}
			open, ok, err := door.LoadOpen(station)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no door is open yet. Start one with: aocn open 1")
			}
			if err := ensureSignedIn(cmd); err != nil {
				return err
			}
			state, err := api.Hints(cmd.Context(), open.Number)
			if err != nil {
				var closed *api.ClosedError
				if errors.As(err, &closed) {
					return fmt.Errorf("not yet: %w", closed)
				}
				return err
			}
			out := cmd.OutOrStdout()
			if state.Total == 0 {
				fmt.Fprintln(out, "This door has no hints.")
				return nil
			}

			opened := len(state.Hints)
			switch {
			case want > state.Total:
				return fmt.Errorf("door %d has %d hints", open.Number, state.Total)
			case want > 0 && want <= opened:
				printHint(out, screenHere().styled, want, state.Total, state.Hints[want-1])
				return nil
			case want > opened+1:
				return fmt.Errorf("hints come in order: open hint %d first, with: aocn hint", opened+1)
			case state.Next == nil:
				fmt.Fprintf(out, "You opened all %d hints. Here they are again.\n", state.Total)
				for i, h := range state.Hints {
					fmt.Fprintln(out)
					printHint(out, screenHere().styled, i+1, state.Total, h)
				}
				return nil
			}

			next := *state.Next
			if next.Cost > 0 && !yes {
				question := fmt.Sprintf("Hint %d of %d costs %d points.", next.Hint, state.Total, next.Cost)
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("%s To open it, run: aocn hint --yes", question)
				}
				fmt.Fprintf(out, "%s Show it? [y/N] ", question)
				answer, _ := stdin.read(cmd.Context())
				if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
					fmt.Fprintln(out, "Kept it closed. Nothing was spent.")
					return nil
				}
				fmt.Fprintln(out)
			}

			state, cost, err := api.OpenHint(cmd.Context(), open.Number, next.Hint, next.Cost)
			if err != nil {
				return err
			}
			printHint(out, screenHere().styled, next.Hint, state.Total, state.Hints[next.Hint-1])
			fmt.Fprintln(out)
			fmt.Fprintln(out, hintCostLine(cost, state))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "open the next hint without asking what it costs")
	return cmd
}

// printHint shows hint number n (from 1) of total. POLARIS-7 gives the hints, so it looks like
// her line in the station's terminal: her name, a quiet ">", and what she says, every line
// under the first.
//
//	Hint 1 of 3
//	POLARIS-7 > The first line.
//	            The second line.
func printHint(out io.Writer, styled bool, n, total int, hint string) {
	name, sep, title, end := "POLARIS-7", ">", fmt.Sprintf("Hint %d of %d", n, total), ""
	if styled {
		name, sep, title, end = bold+name+reset, muted+sep+reset, muted+title+reset, reset
	}
	fmt.Fprintf(out, "%s\n", title)
	indent := strings.Repeat(" ", len("POLARIS-7 > "))
	for i, line := range strings.Split(strings.TrimSpace(hint), "\n") {
		if i == 0 {
			fmt.Fprintf(out, "%s %s %s%s\n", name, sep, line, end)
			continue
		}
		fmt.Fprintf(out, "%s%s\n", indent, line)
	}
}

// hintCostLine says what opening a hint cost, and what's left.
func hintCostLine(cost int, state api.HintState) string {
	var paid string
	switch {
	case cost > 0:
		paid = fmt.Sprintf("That cost %d points.", cost)
	case state.Fixed:
		paid = "Free: you fixed this door already."
	default:
		paid = "Free."
	}
	if state.Next == nil {
		return paid + " That was the last hint."
	}
	return paid + " Stuck again? aocn hint opens the next one."
}

// scoreLine says what a fixed door is worth: "Door 3: 95 points (1 hint), 1 failed verify."
func scoreLine(number int, s api.Score) string {
	line := fmt.Sprintf("Door %d: %d points", number, s.Points)
	if s.Hints > 0 {
		line += fmt.Sprintf(" (%s)", plural(s.Hints, "hint", "hints"))
	}
	if s.FailedVerifies > 0 {
		line += ", " + plural(s.FailedVerifies, "failed verify", "failed verifies")
	}
	if s.Preview {
		line += ", once it opens"
	}
	return line + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
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
