package door

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFolder(t *testing.T) {
	dir := t.TempDir()
	if folder, ok, err := OpenFolder(dir); err != nil || ok {
		t.Fatalf("nothing opened: got %q, %v, %v, want nothing", folder, ok, err)
	}

	if err := SaveOpen(dir, Open{Number: 1, Folder: "01-example"}); err != nil {
		t.Fatal(err)
	}
	if folder, ok, err := OpenFolder(dir); err != nil || ok {
		t.Fatalf("folder missing: got %q, %v, %v, want nothing", folder, ok, err)
	}

	want := filepath.Join(dir, "01-example")
	if err := os.Mkdir(want, 0o755); err != nil {
		t.Fatal(err)
	}
	folder, ok, err := OpenFolder(dir)
	if err != nil || !ok || folder != want {
		t.Errorf("got %q, %v, %v, want %q", folder, ok, err, want)
	}
}
