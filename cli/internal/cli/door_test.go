package cli

import "testing"

func TestDoorFromEnv(t *testing.T) {
	for value, want := range map[string]int{"": 0, "  ": 0, "1": 1, " 23 ": 23} {
		got, err := doorFromEnv(func(string) string { return value })
		if err != nil || got != want {
			t.Errorf("AOCN_DOOR=%q: got %d, %v, want %d", value, got, err, want)
		}
	}
	for _, value := range []string{"one", "0", "-1", "1.5"} {
		if got, err := doorFromEnv(func(string) string { return value }); err == nil {
			t.Errorf("AOCN_DOOR=%q: got %d, want an error", value, got)
		}
	}
}
