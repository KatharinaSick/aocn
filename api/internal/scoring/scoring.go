// Package scoring works out points from what's recorded: every aocn verify and every hint a
// player opened. Nothing here is stored, so changing a rule or a number is a code change and
// nothing needs migrating.
//
// The rules, as players read them on how-it-works:
//
//   - Every door you fix is worth DoorPoints. Hints cost HintCosts, by their position.
//   - Failed verifies cost nothing. They break ties: same points, fewer failed verifies ahead.
//   - When you fix a door doesn't matter.
//
// And the details:
//
//   - A door's points and failed verifies are set the moment you first fix it. Hints and
//     verifies after that are free.
//   - Failed verifies only count on doors you fixed.
//   - A verify that stopped because the workloads weren't ready yet doesn't count.
//   - A previewer's pass from before the opening is a fix once the door opens, and their
//     failed verifies from before it don't count. Their hints cost like everyone's.
//   - A leaderboard may have a deadline: a fix after it is worth nothing there.
package scoring

import (
	"sort"
	"strings"
	"time"
)

// DoorPoints is what a fixed door is worth before hints.
const DoorPoints = 100

// HintCosts is what each of a door's hints costs, by position: the first is a nudge, the
// last nearly the solution. Together they're half a door, which is also the least a fixed
// door is worth.
var HintCosts = [...]int{5, 15, 30}

// HintCost is what the hint at index (0-based) costs. Anything past the last costs nothing:
// there is nothing to open.
func HintCost(index int) int {
	if index < 0 || index >= len(HintCosts) {
		return 0
	}
	return HintCosts[index]
}

// Run is one aocn verify, as the API recorded it.
type Run struct {
	At     time.Time
	Passed bool
	// NotReady means it stopped at a ready check: the workloads weren't up yet. Never set on
	// a run that passed.
	NotReady bool
}

// Hint is one hint a player opened: its index (0-based) and when.
type Hint struct {
	Index int
	At    time.Time
}

// Door is what one door is worth to one player.
type Door struct {
	Fixed bool
	// FixedAt is when it counts as fixed: the first pass, or the opening for a previewer who
	// passed before it.
	FixedAt time.Time
	// Points is 0 unless Fixed.
	Points int
	// Hints is how many hints cost points, FailedVerifies how many failed verifies count.
	// Both are 0 unless Fixed.
	Hints          int
	FailedVerifies int
}

// Score works out one door for one player from their runs and hints, in any order. until is
// a leaderboard's deadline: a fix after it is worth nothing. The zero time means none.
//
// Whether the door is open yet is the caller's to decide: a previewer's pass from before the
// opening is a fix here, at the opening.
//
// Runs after the first pass, runs that weren't ready and every run of a door never passed
// change nothing here. results.Progress relies on that and doesn't load them.
func Score(opensAt, until time.Time, runs []Run, hints []Hint) Door {
	var firstPass time.Time
	for _, r := range runs {
		if r.Passed && (firstPass.IsZero() || r.At.Before(firstPass)) {
			firstPass = r.At
		}
	}
	if firstPass.IsZero() {
		return Door{}
	}
	fixedAt := firstPass
	if fixedAt.Before(opensAt) {
		fixedAt = opensAt
	}
	if !until.IsZero() && fixedAt.After(until) {
		return Door{}
	}

	// Failed verifies count between the opening and the first pass, hints up to the first
	// pass: a previewer pays for theirs too.
	d := Door{Fixed: true, FixedAt: fixedAt, Points: DoorPoints}
	for _, r := range runs {
		if !r.Passed && !r.NotReady && !r.At.Before(opensAt) && r.At.Before(firstPass) {
			d.FailedVerifies++
		}
	}
	// Each hint once, however often it was recorded.
	seen := map[int]bool{}
	for _, h := range hints {
		if seen[h.Index] || !h.At.Before(firstPass) {
			continue
		}
		seen[h.Index] = true
		d.Hints++
		d.Points -= HintCost(h.Index)
	}
	return d
}

// NextHintCost is what opening the hint at index costs now: nothing once the door is fixed,
// and its price otherwise, also for a previewer before the door opens.
func NextHintCost(fixed bool, index int) int {
	if fixed {
		return 0
	}
	return HintCost(index)
}

// HintCostAt is what a hint opened at at costs, given the door's runs: nothing once it's
// fixed, its price otherwise. It agrees with Score, which counts hints up to the first pass.
func HintCostAt(at time.Time, runs []Run, index int) int {
	for _, r := range runs {
		if r.Passed && !r.At.After(at) {
			return 0
		}
	}
	return HintCost(index)
}

// Opening is when a door opens.
type Opening struct {
	Door    int
	OpensAt time.Time
}

// Progress is everything recorded for one player, by door.
type Progress struct {
	Runs  map[int][]Run
	Hints map[int][]Hint
}

// Total is one player's season: every door that's open at the time asked, added up.
type Total struct {
	Points         int
	FailedVerifies int
	Fixed          int
	// Doors has every open door, fixed or not.
	Doors map[int]Door
}

// Season adds up a player's doors that are open at now. until is a leaderboard's deadline,
// the zero time for none.
func Season(openings []Opening, now, until time.Time, p Progress) Total {
	t := Total{Doors: map[int]Door{}}
	for _, o := range openings {
		if now.Before(o.OpensAt) {
			continue
		}
		d := Score(o.OpensAt, until, p.Runs[o.Door], p.Hints[o.Door])
		t.Doors[o.Door] = d
		if d.Fixed {
			t.Fixed++
			t.Points += d.Points
			t.FailedVerifies += d.FailedVerifies
		}
	}
	return t
}

// Entry is one player on a leaderboard.
type Entry struct {
	ID    int64
	Login string
	Total Total
}

// Ranked is an entry with its place.
type Ranked struct {
	Entry
	// Rank is shared by everyone with the same points and failed verifies: 1, 2, 3, 3, 5.
	Rank int
}

// Rank orders entries by points, then fewer failed verifies, and gives everyone with the
// same of both the same rank. Within a rank, names are in alphabetical order. There's no
// third tie-break: anything based on time would bring speed back.
func Rank(entries []Entry) []Ranked {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Total.Points != b.Total.Points {
			return a.Total.Points > b.Total.Points
		}
		if a.Total.FailedVerifies != b.Total.FailedVerifies {
			return a.Total.FailedVerifies < b.Total.FailedVerifies
		}
		if la, lb := strings.ToLower(a.Login), strings.ToLower(b.Login); la != lb {
			return la < lb
		}
		return a.ID < b.ID
	})
	ranked := make([]Ranked, len(sorted))
	for i, e := range sorted {
		rank := i + 1
		if i > 0 {
			prev := ranked[i-1]
			if prev.Total.Points == e.Total.Points && prev.Total.FailedVerifies == e.Total.FailedVerifies {
				rank = prev.Rank
			}
		}
		ranked[i] = Ranked{Entry: e, Rank: rank}
	}
	return ranked
}
