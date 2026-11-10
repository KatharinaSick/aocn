// Package schedule reads schedule.yaml at the repository root, the one place a date
// lives: when each door opens. The website reads the same file (web/src/lib/schedule.ts)
// and must accept and reject exactly what this does; the shared files in testdata/ at
// the repository root check that.
//
// Dates are decoded as strings and checked here, never left to the YAML decoder: a
// date needs a date, a time and an offset, or it is rejected.
package schedule

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The same pattern as on the website: seconds, no fractions, and Z or a ±HH:MM offset.
var datePattern = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(Z|[+-](\d{2}):(\d{2}))$`)

// doorNumber is how a door's number is written: 1, 3, … 23, never 01, +1 or 1.0. A door's
// number is its day in December, written out rather than worked out from its date, so a
// rehearsal can move a date without renumbering the door.
var doorNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

// file is schedule.yaml as written. Doors are a node rather than a map, so their order in
// the file is kept and checked, and a null date is seen instead of dropped.
type file struct {
	Doors        yaml.Node `yaml:"doors"`
	ChristmasEve struct {
		BeforeSeason string `yaml:"beforeSeason"`
		Finale       string `yaml:"finale"`
	} `yaml:"christmasEve"`
}

// Schedule says when each door opens. Every method takes now instead of reading the
// clock, so the schedule can be tested at any moment.
type Schedule struct {
	// doors in the order of the file: by number, each opening after the one before.
	doors []door

	// The moments POLARIS-7's incident counter on the website counts from. Nothing in
	// the API uses them; they are read so a broken one is rejected here too.
	christmasEveBeforeSeason time.Time
	christmasEveFinale       time.Time
}

// Load reads and checks the schedule at path.
func Load(path string) (*Schedule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse checks a schedule. The error names the door or key that is wrong.
func Parse(data []byte) (*Schedule, error) {
	var f file
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	// A misspelled or invented key is an error, not something to ignore.
	decoder.KnownFields(true)
	if err := decoder.Decode(&f); err != nil {
		return nil, err
	}
	// Decode reads only the first document, so anything after a --- would be ignored. A
	// trailing --- counts too: it starts a second, empty document, as on the website.
	var rest yaml.Node
	if err := decoder.Decode(&rest); err == nil {
		return nil, errors.New("must be a single YAML document")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}

	if f.Doors.Kind != yaml.MappingNode || len(f.Doors.Content) == 0 {
		return nil, errors.New("doors: must map door numbers to dates, with at least one door")
	}

	s := &Schedule{}
	for i := 0; i < len(f.Doors.Content); i += 2 {
		key, value := f.Doors.Content[i], f.Doors.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Style != 0 || !doorNumber.MatchString(key.Value) {
			return nil, fmt.Errorf(`doors: "%s" is not a door number, like 1 or 23`, key.Value)
		}
		number, err := strconv.Atoi(key.Value)
		if err != nil {
			return nil, fmt.Errorf(`doors: "%s" is not a door number, like 1 or 23`, key.Value)
		}
		label := fmt.Sprintf("door %d", number)
		if len(s.doors) > 0 {
			previous := s.doors[len(s.doors)-1]
			if number == previous.number {
				return nil, fmt.Errorf("%s: listed twice", label)
			}
			if number < previous.number {
				return nil, fmt.Errorf("%s: listed after door %d; list the doors by number", label, previous.number)
			}
		}
		if value.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("%s: not a date", label)
		}
		text := value.Value
		if value.Tag == "!!null" {
			text = ""
		}
		opensAt, err := parseDate(text, label)
		if err != nil {
			return nil, err
		}
		if len(s.doors) > 0 {
			previous := s.doors[len(s.doors)-1]
			if !opensAt.After(previous.opensAt) {
				return nil, fmt.Errorf("%s: opens at %s, not after door %d", label, text, previous.number)
			}
		}
		s.doors = append(s.doors, door{number: number, opensAt: opensAt})
	}

	var err error
	if s.christmasEveBeforeSeason, err = parseDate(f.ChristmasEve.BeforeSeason, "christmasEve.beforeSeason"); err != nil {
		return nil, err
	}
	if s.christmasEveFinale, err = parseDate(f.ChristmasEve.Finale, "christmasEve.finale"); err != nil {
		return nil, err
	}
	return s, nil
}

func parseDate(text, label string) (time.Time, error) {
	if text == "" {
		return time.Time{}, fmt.Errorf("%s: missing date", label)
	}

	match := datePattern.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}, fmt.Errorf(`%s: "%s" needs a date, a time and an offset, like 2026-12-01T06:00:00+01:00`, label, text)
	}

	t, err := time.Parse(time.RFC3339, text)
	if err != nil || !offsetInRange(match) {
		return time.Time{}, fmt.Errorf(`%s: "%s" is not a valid date`, label, text)
	}
	return t, nil
}

// offsetInRange checks the ±HH:MM offset, which time.Parse does not bound as tightly
// as the website does.
func offsetInRange(match []string) bool {
	if match[7] == "Z" {
		return true
	}
	hours, _ := strconv.Atoi(match[8])
	minutes, _ := strconv.Atoi(match[9])
	return hours <= 23 && minutes <= 59
}

// door is one door: its number, which is its day in December, and when it opens.
type door struct {
	number  int
	opensAt time.Time
}

// Doors is how many doors there are.
func (s *Schedule) Doors() int {
	return len(s.doors)
}

// Numbers are the doors' numbers, in order: 1, 3, 5, … for a door every other day.
func (s *Schedule) Numbers() []int {
	numbers := make([]int, len(s.doors))
	for i, d := range s.doors {
		numbers[i] = d.number
	}
	return numbers
}

// OpensAt is when a door opens. A door's number is its day in December.
func (s *Schedule) OpensAt(number int) (time.Time, error) {
	for _, d := range s.doors {
		if d.number == number {
			return d.opensAt, nil
		}
	}
	return time.Time{}, fmt.Errorf("there is no door %d; the doors are %s", number, s.list())
}

// list names the doors for an error: "1, 3, 5 … 23".
func (s *Schedule) list() string {
	numbers := s.Numbers()
	if len(numbers) <= 3 {
		return strings.Trim(fmt.Sprint(numbers), "[]")
	}
	return fmt.Sprintf("%d, %d, %d … %d", numbers[0], numbers[1], numbers[2], numbers[len(numbers)-1])
}

// IsOpen reports whether a door is open at now. This decides whether a door's content
// may be served: anything about a door that is not open must not be.
func (s *Schedule) IsOpen(door int, now time.Time) (bool, error) {
	opensAt, err := s.OpensAt(door)
	if err != nil {
		return false, err
	}
	return !now.Before(opensAt), nil
}

// Next is the next door to open after now and when. ok is false once every door is open.
func (s *Schedule) Next(now time.Time) (door int, opensAt time.Time, ok bool) {
	for _, d := range s.doors {
		if d.opensAt.After(now) {
			return d.number, d.opensAt, true
		}
	}
	return 0, time.Time{}, false
}
