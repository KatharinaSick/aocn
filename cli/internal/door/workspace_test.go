package door

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsOpen(t *testing.T) {
	station := t.TempDir()
	if ok, err := IsOpen(station); err != nil || ok {
		t.Fatalf("nothing opened: got %v, %v", ok, err)
	}
	if err := SaveOpen(station, Open{Number: 1, Folder: "01-example"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsOpen(station); err != nil || ok {
		t.Fatalf("opened, but .aocn is gone: got %v, %v", ok, err)
	}
	if err := os.Mkdir(filepath.Join(station, ToolDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsOpen(station); err != nil || !ok {
		t.Errorf("open: got %v, %v", ok, err)
	}
}

func TestMoveAside(t *testing.T) {
	station := t.TempDir()
	for _, name := range []string{"README.md", "manifests/policy.yaml", ".aocn/init.sh", ".gitignore", ".open.json", ".hints-seen.json", ".old/01-example-earlier/notes.txt"} {
		path := filepath.Join(station, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	aside, err := MoveAside(station, "01-example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(aside, filepath.Join(".old", "01-example-")) {
		t.Errorf("moved to %q, want .old/01-example-<time>", aside)
	}
	// The door's files, the player's and aocn's, are aside.
	for _, name := range []string{"README.md", "manifests/policy.yaml", ".aocn/init.sh"} {
		if _, err := os.Stat(filepath.Join(station, aside, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s isn't aside: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(station, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s is still in the station", name)
		}
	}
	// The station's own files stay, and so does what was moved aside before.
	for _, name := range []string{".gitignore", ".open.json", ".hints-seen.json", ".old/01-example-earlier/notes.txt"} {
		if _, err := os.Stat(filepath.Join(station, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s should stay: %v", name, err)
		}
	}
}
