package scoring

import (
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/doors"
)

var opensAt = time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC)

// at is a moment relative to the opening.
func at(d time.Duration) time.Time { return opensAt.Add(d) }

func failed(d time.Duration) Run   { return Run{At: at(d)} }
func notReady(d time.Duration) Run { return Run{At: at(d), NotReady: true} }
func passed(d time.Duration) Run   { return Run{At: at(d), Passed: true} }
func hint(i int, d time.Duration) Hint {
	return Hint{Index: i, At: at(d)}
}

// Every door has exactly as many hints as there are prices.
func TestEveryHintHasAPrice(t *testing.T) {
	if len(HintCosts) != doors.HintsPerDoor {
		t.Fatalf("%d hint costs for %d hints per door", len(HintCosts), doors.HintsPerDoor)
	}
	sum := 0
	for _, c := range HintCosts {
		sum += c
	}
	// The floor of a fixed door is all hints, and it must stay clearly above nothing.
	if sum != DoorPoints/2 {
		t.Errorf("all hints cost %d, want half a door", sum)
	}
}

func TestScore(t *testing.T) {
	const h = time.Hour
	tests := []struct {
		name  string
		until time.Time
		runs  []Run
		hints []Hint
		want  Door
	}{
		// The examples from the rules.
		{"first try, no hints", time.Time{}, []Run{passed(h)}, nil,
			Door{Fixed: true, FixedAt: at(h), Points: 100}},
		{"after 3 failed verifies", time.Time{}, []Run{failed(h), failed(2 * h), failed(3 * h), passed(4 * h)}, nil,
			Door{Fixed: true, FixedAt: at(4 * h), Points: 100, FailedVerifies: 3}},
		{"with the first hint", time.Time{}, []Run{passed(2 * h)}, []Hint{hint(0, h)},
			Door{Fixed: true, FixedAt: at(2 * h), Points: 95, Hints: 1}},
		{"with the first two hints", time.Time{}, []Run{passed(3 * h)}, []Hint{hint(0, h), hint(1, 2*h)},
			Door{Fixed: true, FixedAt: at(3 * h), Points: 80, Hints: 2}},
		{"with all 3 hints", time.Time{}, []Run{passed(4 * h)}, []Hint{hint(0, h), hint(1, 2*h), hint(2, 3*h)},
			Door{Fixed: true, FixedAt: at(4 * h), Points: 50, Hints: 3}},
		{"not fixed", time.Time{}, []Run{failed(h), failed(2 * h)}, []Hint{hint(0, h)},
			Door{}},
		{"nothing", time.Time{}, nil, nil, Door{}},

		// Set the moment of the first fix.
		{"verifies and hints after the fix are free", time.Time{},
			[]Run{failed(h), passed(2 * h), failed(3 * h), passed(4 * h)}, []Hint{hint(0, 3*h), hint(1, 5*h)},
			Door{Fixed: true, FixedAt: at(2 * h), Points: 100, FailedVerifies: 1}},
		{"order doesn't matter", time.Time{},
			[]Run{passed(4 * h), failed(h), passed(2 * h)}, []Hint{hint(1, 3*h), hint(0, h/2)},
			Door{Fixed: true, FixedAt: at(2 * h), Points: 95, Hints: 1, FailedVerifies: 1}},
		{"a hint recorded twice costs once", time.Time{}, []Run{passed(2 * h)}, []Hint{hint(0, h), hint(0, h)},
			Door{Fixed: true, FixedAt: at(2 * h), Points: 95, Hints: 1}},
		{"workloads not ready yet don't count", time.Time{},
			[]Run{notReady(h), notReady(h + time.Minute), failed(2 * h), passed(3 * h)}, nil,
			Door{Fixed: true, FixedAt: at(3 * h), Points: 100, FailedVerifies: 1}},

		// Previewers: what happens before the opening doesn't count.
		{"a pass before the opening is a clean fix at the opening", time.Time{},
			[]Run{failed(-3 * h), failed(-2 * h), passed(-h)}, []Hint{hint(0, -2*h), hint(1, -90*time.Minute)},
			Door{Fixed: true, FixedAt: opensAt, Points: 100}},
		{"hints before the opening are free, after it they cost", time.Time{},
			[]Run{failed(-h), failed(h), passed(2 * h)}, []Hint{hint(0, -h), hint(1, h)},
			Door{Fixed: true, FixedAt: at(2 * h), Points: 85, Hints: 1, FailedVerifies: 1}},

		// A leaderboard's deadline.
		{"a fix before the deadline counts", at(5 * h), []Run{failed(h), passed(2 * h)}, []Hint{hint(0, h)},
			Door{Fixed: true, FixedAt: at(2 * h), Points: 95, Hints: 1, FailedVerifies: 1}},
		{"a fix right at the deadline counts", at(2 * h), []Run{passed(2 * h)}, nil,
			Door{Fixed: true, FixedAt: at(2 * h), Points: 100}},
		{"a fix after the deadline is worth nothing", at(h), []Run{failed(h / 2), passed(2 * h)}, []Hint{hint(0, h/2)},
			Door{}},
		{"a door that opens after the deadline is worth nothing", at(-h), []Run{passed(-2 * h)}, nil,
			Door{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Score(opensAt, tt.until, tt.runs, tt.hints)
			if !got.FixedAt.Equal(tt.want.FixedAt) {
				t.Errorf("fixed at %v, want %v", got.FixedAt, tt.want.FixedAt)
			}
			got.FixedAt, tt.want.FixedAt = time.Time{}, time.Time{}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNextHintCost(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		fixed bool
		index int
		want  int
	}{
		{"first", at(time.Hour), false, 0, 5},
		{"second", at(time.Hour), false, 1, 15},
		{"third", at(time.Hour), false, 2, 30},
		{"none left", at(time.Hour), false, 3, 0},
		{"fixed", at(time.Hour), true, 1, 0},
		{"before the opening", at(-time.Hour), false, 1, 0},
		{"at the opening", opensAt, false, 0, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextHintCost(opensAt, tt.now, tt.fixed, tt.index); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSeason(t *testing.T) {
	const h = time.Hour
	openings := []Opening{
		{Door: 1, OpensAt: opensAt},
		{Door: 3, OpensAt: opensAt.Add(48 * h)},
		{Door: 5, OpensAt: opensAt.Add(96 * h)},
	}
	p := Progress{
		Runs: map[int][]Run{
			1: {failed(h), failed(2 * h), passed(3 * h)},
			3: {failed(49 * h)}, // never fixed: its failed verify doesn't count
			5: {passed(90 * h)}, // a previewer's, before door 5 opens
		},
		Hints: map[int][]Hint{1: {hint(0, h)}},
	}

	// Door 5 isn't open yet: it's not there at all, not even as not fixed.
	got := Season(openings, opensAt.Add(50*h), time.Time{}, p)
	if got.Points != 95 || got.FailedVerifies != 2 || got.Fixed != 1 || len(got.Doors) != 2 {
		t.Errorf("before door 5: %+v", got)
	}
	if d := got.Doors[3]; d.Fixed {
		t.Errorf("door 3: %+v", d)
	}

	// Once it opens, the previewer's pass counts, clean.
	got = Season(openings, opensAt.Add(100*h), time.Time{}, p)
	if got.Points != 195 || got.FailedVerifies != 2 || got.Fixed != 2 || len(got.Doors) != 3 {
		t.Errorf("after door 5: %+v", got)
	}

	// A deadline before door 5 opened leaves it out.
	got = Season(openings, opensAt.Add(100*h), opensAt.Add(60*h), p)
	if got.Points != 95 || got.Fixed != 1 {
		t.Errorf("with a deadline: %+v", got)
	}
}

func TestRank(t *testing.T) {
	entry := func(id int64, login string, points, failedVerifies int) Entry {
		return Entry{ID: id, Login: login, Total: Total{Points: points, FailedVerifies: failedVerifies}}
	}
	// The leaderboard from the proposal, shuffled.
	got := Rank([]Entry{
		entry(5, "erin", 595, 0),
		entry(4, "dave", 650, 9),
		entry(2, "Bob", 650, 9),
		entry(1, "alice", 650, 4),
		entry(3, "carol", 650, 9),
		entry(6, "frank", 0, 0),
	})
	want := []struct {
		login string
		rank  int
	}{{"alice", 1}, {"Bob", 2}, {"carol", 2}, {"dave", 2}, {"erin", 5}, {"frank", 6}}
	if len(got) != len(want) {
		t.Fatalf("got %d entries", len(got))
	}
	for i, w := range want {
		if got[i].Login != w.login || got[i].Rank != w.rank {
			t.Errorf("%d: got %s #%d, want %s #%d", i, got[i].Login, got[i].Rank, w.login, w.rank)
		}
	}
	if len(Rank(nil)) != 0 {
		t.Error("nobody should rank nobody")
	}
}
