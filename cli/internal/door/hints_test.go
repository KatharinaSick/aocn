package door

import (
	"slices"
	"testing"
)

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
