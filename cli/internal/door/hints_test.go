package door

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestHints(t *testing.T) {
	folder := t.TempDir()
	if hints, err := Hints(folder); err != nil || hints != nil {
		t.Fatalf("no hints.yaml: got %q, %v, want no hints", hints, err)
	}

	if err := os.MkdirAll(filepath.Join(folder, ToolDir), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, ToolDir, hintsFile)
	if err := os.WriteFile(file, []byte("- first\n- |\n  second,\n  on two lines\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hints, err := Hints(folder)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second,\non two lines\n"}; !slices.Equal(hints, want) {
		t.Errorf("got %q, want %q", hints, want)
	}

	if err := os.WriteFile(file, []byte("hint: not a list\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Hints(folder); err == nil {
		t.Error("a hints.yaml that isn't a list: got no error")
	}
}

func TestNextHint(t *testing.T) {
	dir := t.TempDir()
	var got []int
	for range 4 {
		i, err := NextHint(dir, 1, 3)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, i)
	}
	if want := []int{0, 1, 2, 2}; !slices.Equal(got, want) {
		t.Errorf("door 1: got %v, want %v", got, want)
	}

	// Every door counts on its own.
	if i, err := NextHint(dir, 2, 3); err != nil || i != 0 {
		t.Errorf("door 2's first hint: got %d, %v, want 0", i, err)
	}
}
