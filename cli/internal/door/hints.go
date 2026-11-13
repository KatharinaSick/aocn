package door

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// seenFile counts the hints each door has shown, by door number. It lives next to the doors
// and outside their folders, so opening a door again doesn't reset it.
const seenFile = ".hints-seen.json"

// NextHint returns the index of the hint to show for door number, out of total, and
// remembers that it was shown. After the last hint, it returns the last one again.
func NextHint(dir string, number, total int) (int, error) {
	seen := map[string]int{}
	data, err := os.ReadFile(filepath.Join(dir, seenFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &seen); err != nil {
			return 0, fmt.Errorf("reading %s: %w", seenFile, err)
		}
	}

	key := strconv.Itoa(number)
	next := nextHint(seen[key], total)
	seen[key] = next + 1
	data, err = json.Marshal(seen)
	if err != nil {
		return 0, err
	}
	return next, os.WriteFile(filepath.Join(dir, seenFile), append(data, '\n'), 0o644)
}

// nextHint is the index to show after seen hints, out of total: the next one, or the last one
// again once all were shown.
func nextHint(seen, total int) int {
	return min(seen, total-1)
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
