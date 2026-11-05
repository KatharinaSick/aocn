// Package cli wires the aocn commands. It only parses arguments; the work happens in
// other packages.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd builds the aocn command with all its subcommands.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "aocn",
		Short: "Play Advent of Cloud Native",
		Long: `aocn opens the doors of Advent of Cloud Native and checks your solutions.

  aocn login        sign in with GitHub
  aocn open 1       start door 1 on a fresh cluster
  aocn verify       check the open door
  aocn hint         get a hint for the open door

More at https://aocn.dev`,
		SilenceUsage: true,
		// main prints errors, so a failed check can exit 1 without an "Error:" line.
		SilenceErrors: true,
	}

	// Completion is turned on by the Codespace's shell setup, so players don't need to see it.
	root.CompletionOptions.HiddenDefaultCmd = true

	// cmd.Println writes to stderr unless an output is set. Results go to stdout.
	root.SetOut(os.Stdout)

	root.AddCommand(
		newLoginCmd(),
		newOpenCmd(),
		newResumeCmd(),
		newVerifyCmd(),
		newCheckCmd(),
		newHintCmd(),
		newVersionCmd(version),
	)
	return root
}
