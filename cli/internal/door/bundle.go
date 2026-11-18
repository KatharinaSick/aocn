// Package door unpacks a door's bundle into the station, the workspace folder the player plays
// in, and remembers which door is open. A station holds one door.
package door

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// A bundle is a .tar.gz with one folder, named NN-<name>, that holds the door's files.
var folderPattern = regexp.MustCompile(`^(\d{2})-[a-z0-9-]+$`)

// BundleFolder returns the name of the bundle's folder, like "01-example", and checks that
// it belongs to door number.
func BundleFolder(bundle string, number int) (string, error) {
	var folder string
	err := walk(bundle, func(name string, _ *tar.Header, _ io.Reader) error {
		top, _, _ := strings.Cut(name, "/")
		if folder == "" {
			folder = top
		} else if top != folder {
			return fmt.Errorf("a bundle has one folder, but this one has %q and %q", folder, top)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	m := folderPattern.FindStringSubmatch(folder)
	if m == nil {
		return "", fmt.Errorf("the bundle's folder must be named NN-<name>, got %q", folder)
	}
	if m[1] != fmt.Sprintf("%02d", number) {
		return "", fmt.Errorf("this bundle is for door %s, not door %d", strings.TrimLeft(m[1], "0"), number)
	}
	return folder, nil
}

// ToolDir is the folder in every door that belongs to aocn: the door's init.sh and workloads.
// The checks and hints aren't in it: aocn verify and aocn hint get them from the API.
const ToolDir = ".aocn"

// Readme is aocn's too: every door's is the same text, pointing to the door's page. In a
// Codespace, it replaces the "your station is starting" page that opened with it, and VS Code
// shows the new one in the same tab. Everything else in the station is the player's.
const Readme = "README.md"

// ours reports whether rel, a path in the door's folder, is aocn's: replaced from the bundle on
// every open, never moved aside as the player's.
func ours(rel string) bool {
	return rel == ToolDir || strings.HasPrefix(rel, ToolDir+"/") || rel == Readme
}

// inDoor returns name without the bundle's folder: "01-example/manifests/a.yaml" is
// "manifests/a.yaml". The folder itself is "".
func inDoor(name string) string {
	_, rel, _ := strings.Cut(name, "/")
	return rel
}

// Extract unpacks the door's files from the bundle right into station, without the bundle's
// folder: a station holds one door. Files that are there already are overwritten, so a station
// left half-unpacked by an open that failed gets whole again.
func Extract(bundle, station string) error {
	if err := os.MkdirAll(station, 0o755); err != nil {
		return err
	}
	return walk(bundle, func(name string, h *tar.Header, r io.Reader) error {
		if rel := inDoor(name); rel != "" {
			return writeEntry(station, rel, h, r)
		}
		return nil
	})
}

// Refresh replaces the station's .aocn and README.md with the bundle's, so a door the player
// opened before gets the newest init.sh and workloads. It doesn't touch the player's files.
func Refresh(bundle, station string) error {
	if err := os.RemoveAll(filepath.Join(station, ToolDir)); err != nil {
		return err
	}
	return walk(bundle, func(name string, h *tar.Header, r io.Reader) error {
		if rel := inDoor(name); ours(rel) {
			return writeEntry(station, rel, h, r)
		}
		return nil
	})
}

// Strangers returns what's in station that's neither the station's own nor in the bundle: a
// folder with other things in it, where unpacking a door would mix its files with someone's.
func Strangers(bundle, station string) ([]string, error) {
	entries, err := os.ReadDir(station)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inBundle := map[string]bool{}
	err = walk(bundle, func(name string, _ *tar.Header, _ io.Reader) error {
		if top, _, _ := strings.Cut(inDoor(name), "/"); top != "" {
			inBundle[top] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var strangers []string
	for _, e := range entries {
		if !stationOwn[e.Name()] && !inBundle[e.Name()] {
			strangers = append(strangers, e.Name())
		}
	}
	return strangers, nil
}

// writeEntry writes one entry of the bundle to dir/name.
func writeEntry(dir, name string, h *tar.Header, r io.Reader) error {
	target := filepath.Join(dir, filepath.FromSlash(name))
	switch h.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// Keep the executable bit for scripts. Everything is the player's to edit.
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode&0o111)|0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	default:
		return fmt.Errorf("the bundle contains %q, which is neither a file nor a folder", name)
	}
}

// walk calls fn for every entry of the bundle, with its cleaned name. It refuses names that
// would end up outside the bundle's folder.
func walk(bundle string, fn func(name string, h *tar.Header, r io.Reader) error) error {
	f, err := os.Open(bundle)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("the bundle isn't a .tar.gz: %w", err)
	}
	tr := tar.NewReader(gz)
	empty := true
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the bundle: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("the bundle contains %q, which is outside its folder", h.Name)
		}
		if err := fn(name, h, tr); err != nil {
			return err
		}
		empty = false
	}
	if empty {
		return fmt.Errorf("the bundle is empty")
	}
	return nil
}
