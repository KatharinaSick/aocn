package schedule

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The same files as web/src/lib/schedule.test.ts reads.
const repo = "../../.."

func load(t *testing.T, path string) *Schedule {
	t.Helper()
	s, err := Load(filepath.Join(repo, path))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func utc(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// Written out in the website's test too: both sides must agree on every moment.
func TestFixtureOpeningTimes(t *testing.T) {
	s := load(t, "testdata/schedule.yaml")

	want := map[int]string{1: "2030-12-01T05:00:00Z", 3: "2030-12-03T05:00:00Z", 5: "2030-12-05T00:30:00Z"}
	if got := s.Numbers(); !slices.Equal(got, []int{1, 3, 5}) {
		t.Fatalf("Numbers() = %v, want [1 3 5]", got)
	}
	for door, w := range want {
		opensAt, err := s.OpensAt(door)
		if err != nil {
			t.Fatal(err)
		}
		if !opensAt.Equal(utc(t, w)) {
			t.Errorf("door %d opens at %s, want %s", door, opensAt.UTC().Format(time.RFC3339), w)
		}
	}

	if !s.christmasEveBeforeSeason.Equal(utc(t, "2029-12-24T05:00:00Z")) {
		t.Errorf("christmasEve.beforeSeason = %s", s.christmasEveBeforeSeason.UTC())
	}
	if !s.christmasEveFinale.Equal(utc(t, "2030-12-24T05:00:00Z")) {
		t.Errorf("christmasEve.finale = %s", s.christmasEveFinale.UTC())
	}
}

func TestFixtureDoorOneOpensOnTheSecond(t *testing.T) {
	s := load(t, "testdata/schedule.yaml")
	opensAt := utc(t, "2030-12-01T05:00:00Z")

	before := opensAt.Add(-time.Second)
	if open, _ := s.IsOpen(1, before); open {
		t.Error("door 1 open one second before it opens")
	}
	if door, at, ok := s.Next(before); !ok || door != 1 || !at.Equal(opensAt) {
		t.Errorf("Next one second before door 1 = %d, %s, %v", door, at, ok)
	}

	if open, _ := s.IsOpen(1, opensAt); !open {
		t.Error("door 1 closed at the moment it opens")
	}
	if door, _, ok := s.Next(opensAt); !ok || door != 3 {
		t.Errorf("Next when door 1 opens = %d, %v, want door 3", door, ok)
	}
}

func TestFixtureAfterTheLastDoor(t *testing.T) {
	s := load(t, "testdata/schedule.yaml")
	now := utc(t, "2030-12-05T00:30:00Z")

	for _, door := range s.Numbers() {
		if open, _ := s.IsOpen(door, now); !open {
			t.Errorf("door %d closed after the last door opened", door)
		}
	}
	if door, _, ok := s.Next(now); ok {
		t.Errorf("Next after the last door = door %d", door)
	}
}

func TestNoSuchDoor(t *testing.T) {
	s := load(t, "testdata/schedule.yaml")
	// 2 and 4 are days without a door.
	for _, door := range []int{0, -1, 2, 4, 6} {
		if _, err := s.OpensAt(door); err == nil {
			t.Errorf("OpensAt(%d) accepted", door)
		}
		if _, err := s.IsOpen(door, utc(t, "2031-01-01T00:00:00Z")); err == nil {
			t.Errorf("IsOpen(%d) accepted", door)
		}
	}
}

// What must always hold for the real schedule. The dates themselves are not repeated
// here: schedule.yaml is where they live.
func TestRealSchedule(t *testing.T) {
	s := load(t, "schedule.yaml")

	if s.Doors() != 12 {
		t.Errorf("%d doors, want 12", s.Doors())
	}
	cet := time.FixedZone("CET", 60*60)
	for i, d := range s.doors {
		if i > 0 && !d.opensAt.After(s.doors[i-1].opensAt) {
			t.Errorf("door %d does not open after door %d", d.number, s.doors[i-1].number)
		}
		if !d.opensAt.Truncate(time.Minute).Equal(d.opensAt) {
			t.Errorf("door %d does not open on the minute", d.number)
		}
		// Only for the real schedule: a rehearsal may move a date without renumbering.
		if day := d.opensAt.In(cet); day.Month() != time.December || day.Day() != d.number {
			t.Errorf("door %d opens on %s, not on Dec %d", d.number, day.Format("Jan 2"), d.number)
		}
	}
	if !s.christmasEveBeforeSeason.Before(s.doors[0].opensAt) {
		t.Error("christmasEve.beforeSeason is not before door 1")
	}
	if !s.christmasEveFinale.After(s.doors[len(s.doors)-1].opensAt) {
		t.Error("christmasEve.finale is not after the last door")
	}
}

// Each file lists on its first line the UTC moment of every door; the website's test
// reads the same files.
func TestValidSchedules(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repo, "testdata/schedule-valid/*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no valid schedules in testdata/schedule-valid: %v", err)
	}
	expected := regexp.MustCompile(`(?m)^# utc: (.*)$`)

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			match := expected.FindSubmatch(data)
			if match == nil {
				t.Fatal(`no "# utc:" line`)
			}
			s, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Fields(string(match[1]))
			if s.Doors() != len(want) {
				t.Fatalf("%d doors, want %d", s.Doors(), len(want))
			}
			for i, w := range want {
				if !s.doors[i].opensAt.Equal(utc(t, w)) {
					t.Errorf("door %d opens at %s, want %s", s.doors[i].number, s.doors[i].opensAt.UTC().Format(time.RFC3339), w)
				}
			}
		})
	}
}

// Each file says on its first line what the error must contain; the website's test
// reads the same files.
func TestBrokenSchedules(t *testing.T) {
	dir := filepath.Join(repo, "testdata/schedule-broken")
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no broken schedules in %s: %v", dir, err)
	}
	expected := regexp.MustCompile(`(?m)^# error: (.*)$`)

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			match := expected.FindSubmatch(data)
			if match == nil {
				t.Fatal(`no "# error:" line`)
			}
			_, err = Parse(data)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), string(match[1])) {
				t.Errorf("error %q does not contain %q", err, match[1])
			}
		})
	}
}

func TestLoadNamesTheFile(t *testing.T) {
	path := filepath.Join(repo, "testdata/schedule-broken/no-offset.yaml")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("error %v does not name %s", err, path)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing file accepted")
	}
}
