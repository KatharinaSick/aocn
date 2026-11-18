// Package auth signs players in with GitHub's device flow and keeps the token the API hands
// out for it (see api.SignIn). That token carries the player's GitHub user ID, so a result is
// saved under their GitHub account without the API asking GitHub each time.
package auth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// tokenPath is where the token is kept: ~/.config/aocn/token in a Codespace. It's outside the
// workspace, so it can't end up next to the player's files. It survives a Codespace stop and
// start, not a rebuild; signing in again takes a few seconds.
func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aocn", "token"), nil
}

// Token returns the saved token, or "" if the player hasn't signed in.
func Token() (string, error) {
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveToken keeps the token, readable only by the player.
func SaveToken(token string) error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}
