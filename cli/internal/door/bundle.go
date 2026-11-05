// Package door unpacks door bundles into the workspace and remembers which door is open.
package door

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
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

// ToolDir is the folder in every door that belongs to aocn: the door's init.sh, verify.sh
// and workloads. Everything else in a door's folder is the player's.
const ToolDir = ".aocn"

// Extract unpacks the bundle into dir. The bundle's folder ends up as dir/<folder>, which
// must not exist yet. If unpacking fails, the half-unpacked folder is removed again.
//
// It doesn't unpack somewhere else and rename: on a bind-mounted workspace (podman on macOS),
// renaming a folder tree doesn't reliably move everything.
func Extract(bundle, dir string) (err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var folder string
	defer func() {
		if err != nil && folder != "" {
			os.RemoveAll(filepath.Join(dir, folder))
		}
	}()

	return walk(bundle, func(name string, h *tar.Header, r io.Reader) error {
		if folder == "" {
			folder, _, _ = strings.Cut(name, "/")
			if _, err := os.Stat(filepath.Join(dir, folder)); err == nil {
				existing := folder
				folder = "" // not ours to remove
				return fmt.Errorf("doors/%s exists already", existing)
			}
		}
		return writeEntry(dir, name, h, r)
	})
}

// Refresh replaces dir/folder/.aocn with the one from the bundle, so a door the player opened
// before gets the newest init.sh, checks and workloads. It doesn't touch the player's files.
func Refresh(bundle, dir, folder string) error {
	if err := os.RemoveAll(filepath.Join(dir, folder, ToolDir)); err != nil {
		return err
	}
	prefix := folder + "/" + ToolDir
	return walk(bundle, func(name string, h *tar.Header, r io.Reader) error {
		if name != prefix && !strings.HasPrefix(name, prefix+"/") {
			return nil
		}
		return writeEntry(dir, name, h, r)
	})
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
