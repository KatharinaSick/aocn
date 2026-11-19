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
)

// Station is the folder the player plays in, which holds the open door's files: AOCN_WORKSPACE
// in the devcontainer (play/ in KatharinaSick/aocn), or the current folder anywhere else. A
// station holds one door. Every door has its own Codespace, so its own station.
func Station() (string, error) {
	if workspace := os.Getenv("AOCN_WORKSPACE"); workspace != "" {
		return workspace, nil
	}
	return os.Getwd()
}

// AsideDir is where a door's files go when the player starts over: nothing is ever deleted.
const AsideDir = ".old"

// stationOwn is what belongs to the station, not to the door in it: play/'s .gitignore and
// README.md (the "your station is starting" page, which the door's README.md replaces), the
// file aocn keeps its state in, and the files moved aside.
var stationOwn = map[string]bool{
	".gitignore": true, Readme: true, stateFile: true, oldHintsFile: true, AsideDir: true,
}

// oldHintsFile is where aocn before 0.6.0 counted the hints it showed. The API keeps that
// now, but a station from back then may still have it.
const oldHintsFile = ".hints-seen.json"

// Open is the door that runs in the cluster right now.
type Open struct {
	Number int    `json:"number"`
	Folder string `json:"folder"`
}

// stateFile lives in the station, so it survives a container rebuild like the door's files do.
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

// IsOpen reports whether a door is open in station: it was opened, and its .aocn is there.
func IsOpen(station string) (bool, error) {
	_, ok, err := LoadOpen(station)
	if err != nil || !ok {
		return false, err
	}
	info, err := os.Stat(filepath.Join(station, ToolDir))
	return err == nil && info.IsDir(), nil
}

// MoveAside moves the door's files in station, the player's and aocn's, to
// .old/<folder>-<time>/, so starting over never deletes what the player wrote. The station's
// own files stay. It returns where the files went, like ".old/01-example-20261201-060000".
func MoveAside(station, folder string) (string, error) {
	aside := filepath.Join(AsideDir, folder+"-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Join(station, aside), 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(station)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		// The door's README.md goes too, so the folder aside is the whole door. The open after
		// this writes it again.
		if stationOwn[e.Name()] && e.Name() != Readme {
			continue
		}
		if err := os.Rename(filepath.Join(station, e.Name()), filepath.Join(station, aside, e.Name())); err != nil {
			return "", err
		}
	}
	return aside, nil
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

// KindConfig returns the path of folder's .aocn/kind.yaml, or "" if the door runs on a plain
// cluster.
func KindConfig(folder string) string {
	path := filepath.Join(folder, ToolDir, "kind.yaml")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}
