package door

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/katharinasick/aocn/cli/internal/check"
)

// Dir is the folder in the workspace that holds one folder per door. Doors stay after the
// next one opens, so players keep their files and notes.
func Dir() (string, error) {
	workspace := os.Getenv("AOCN_WORKSPACE")
	if workspace == "" {
		var err error
		if workspace, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	return filepath.Join(workspace, "doors"), nil
}

// Open is the door that runs in the cluster right now.
type Open struct {
	Number int    `json:"number"`
	Folder string `json:"folder"`
}

// stateFile lives next to the doors, so it survives a container rebuild like they do.
const stateFile = ".open.json"

// SaveOpen remembers which door runs in the cluster.
func SaveOpen(dir string, open Open) error {
	data, err := json.Marshal(open)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stateFile), append(data, '\n'), 0o644)
}

// LoadOpen returns the door that runs in the cluster, or false if no door was opened yet.
func LoadOpen(dir string) (Open, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Open{}, false, nil
	}
	if err != nil {
		return Open{}, false, err
	}
	var open Open
	if err := json.Unmarshal(data, &open); err != nil {
		return Open{}, false, fmt.Errorf("reading %s: %w", stateFile, err)
	}
	return open, true, nil
}

// MoveAside renames dir/folder to dir/folder.old-<time>, so starting a door fresh never
// deletes what the player wrote. It returns the new name.
func MoveAside(dir, folder string) (string, error) {
	aside := folder + ".old-" + time.Now().Format("20060102-150405")
	if err := os.Rename(filepath.Join(dir, folder), filepath.Join(dir, aside)); err != nil {
		return "", err
	}
	return aside, nil
}

// Exists reports whether dir/folder is there.
func Exists(dir, folder string) bool {
	_, err := os.Stat(filepath.Join(dir, folder))
	return err == nil
}

// Init runs the door's .aocn/init.sh, which brings the door to its start state. It's safe to
// run again: it applies the player's current files.
func Init(ctx context.Context, out io.Writer, folder string) error {
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(ToolDir, "init.sh"))
	cmd.Dir = folder
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the door's %s/init.sh failed: %w", ToolDir, err)
	}
	return nil
}

// Verify runs the door's .aocn/verify.sh. Every check in it appends its result to results.
func Verify(ctx context.Context, out io.Writer, folder, results string) error {
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(ToolDir, "verify.sh"))
	cmd.Dir = folder
	cmd.Env = append(os.Environ(), check.ResultsEnv+"="+results)
	cmd.Stdout = out
	cmd.Stderr = out
	// On Ctrl+C, the running check gets the signal from the terminal too and cleans up. Give
	// it time instead of killing the script right away.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 30 * time.Second
	return cmd.Run()
}
