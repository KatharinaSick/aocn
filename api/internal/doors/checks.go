package doors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Check is one check from a door's checks.yaml. Exactly one of Ready, HTTP and Job is set.
// The CLI runs them, and sends back one result per ID.
//
//   - ready: [apps, db]        every Deployment in the namespaces is ready. If not, the
//     checks after it don't run.
//   - http: {method, to, from, expect, data}   a request from a workload
//   - job: apps/cronjob/cleanup                a fresh run of the CronJob completes
//
// Checks can be grouped under a line of the door's mission in page.yaml, by its number:
//
//   - goal: 1
//     checks:
//   - http: …
//
// aocn verify then shows one line per goal, not what each check sends, so it doesn't give the
// solution away.
type Check struct {
	// ID is a hash of the check itself, so a result names exactly the check it's for. Editing
	// a check changes its ID. Moving it to another goal doesn't.
	ID    string     `yaml:"-" json:"id,omitempty"`
	Ready []string   `yaml:"ready,omitempty" json:"ready,omitempty"`
	HTTP  *HTTPCheck `yaml:"http,omitempty" json:"http,omitempty"`
	Job   string     `yaml:"job,omitempty" json:"job,omitempty"`
	// Goal is the number of the mission line the check belongs to, from 1, or 0 for none.
	Goal int `yaml:"-" json:"-"`
}

// entry is one item of checks.yaml: a check, or a goal with its checks.
type entry struct {
	Check  `yaml:",inline"`
	Goal   int     `yaml:"goal,omitempty"`
	Checks []Check `yaml:"checks,omitempty"`
}

// HTTPCheck sends a request from a workload and passes if the answer is the expected one.
// The CLI documents what each field accepts; the API only checks they're there.
type HTTPCheck struct {
	Method string `yaml:"method" json:"method"`
	To     string `yaml:"to" json:"to"`
	From   string `yaml:"from" json:"from"`
	Expect string `yaml:"expect" json:"expect"`
	Data   string `yaml:"data,omitempty" json:"data,omitempty"`
}

var (
	methodPattern = regexp.MustCompile(`^[A-Z]+$`)
	expectPattern = regexp.MustCompile(`^(blocked|[1-5]xx|[1-5][0-9]{2})$`)
)

// ParseChecks reads a checks.yaml: a list of checks, each with its ID set. Like page.yaml, an
// unknown key is an error, so a typo can't quietly drop a check.
func ParseChecks(data []byte) ([]Check, error) {
	var entries []entry
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&entries); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("checks.yaml is empty")
		}
		return nil, err
	}
	var rest yaml.Node
	if err := decoder.Decode(&rest); err == nil {
		return nil, errors.New("checks.yaml must be a single YAML document")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	// where names each check as it's written in the file, for the errors below: "check 3" is
	// the file's third item, "goal 2, check 1" the first check under a goal.
	var checks []Check
	var where []string
	for i, e := range entries {
		switch {
		case e.Goal == 0 && e.Checks == nil:
			checks = append(checks, e.Check)
			where = append(where, fmt.Sprintf("check %d", i+1))
		case e.Goal < 1:
			return nil, fmt.Errorf("checks.yaml: item %d: checks need a goal, the number of a mission line in page.yaml", i+1)
		case e.Ready != nil || e.HTTP != nil || e.Job != "":
			return nil, fmt.Errorf("checks.yaml: item %d: a goal lists its checks under checks", i+1)
		case len(e.Checks) == 0:
			return nil, fmt.Errorf("checks.yaml: goal %d has no checks", e.Goal)
		default:
			for j, c := range e.Checks {
				if c.Ready != nil {
					return nil, fmt.Errorf("checks.yaml: goal %d: a ready check can't be part of a goal", e.Goal)
				}
				c.Goal = e.Goal
				checks = append(checks, c)
				where = append(where, fmt.Sprintf("goal %d, check %d", e.Goal, j+1))
			}
		}
	}
	if len(checks) == 0 {
		return nil, errors.New("checks.yaml has no checks")
	}

	seen := map[string]string{}
	for i := range checks {
		c := &checks[i]
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("checks.yaml: %s: %w", where[i], err)
		}
		c.ID = c.hash()
		if first, ok := seen[c.ID]; ok {
			return nil, fmt.Errorf("checks.yaml: %s is the same as %s", where[i], first)
		}
		seen[c.ID] = where[i]
	}
	return checks, nil
}

func (c Check) validate() error {
	kinds := 0
	if c.Ready != nil {
		kinds++
		if len(c.Ready) == 0 {
			return errors.New("ready needs at least one namespace")
		}
		for _, ns := range c.Ready {
			if strings.TrimSpace(ns) == "" {
				return errors.New("ready has an empty namespace")
			}
		}
	}
	if c.HTTP != nil {
		kinds++
		h := c.HTTP
		switch {
		case !methodPattern.MatchString(h.Method):
			return fmt.Errorf("http: method must be like GET or POST, got %q", h.Method)
		case h.To == "":
			return errors.New("http: to is required")
		case h.From == "":
			return errors.New("http: from is required")
		case !expectPattern.MatchString(h.Expect):
			return fmt.Errorf("http: expect must be a status code like 403, a class like 2xx, or blocked, got %q", h.Expect)
		}
	}
	if c.Job != "" {
		kinds++
	}
	if kinds != 1 {
		return errors.New("needs exactly one of ready, http or job")
	}
	return nil
}

// hash is the check's ID: the first 16 hex digits of the SHA-256 of its JSON without the ID
// and the goal.
// encoding/json writes struct fields in a fixed order, so the same check always hashes the
// same.
func (c Check) hash() string {
	c.ID, c.Goal = "", 0
	data, _ := json.Marshal(c) // a struct of strings can't fail
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// ChecksName is where the upload puts a door's checks.
func ChecksName(door int) string { return fmt.Sprintf("doors/%02d/checks.yaml", door) }

// Checks reads each door's checks from storage and keeps them for a minute, like Pages. It
// never decides whether they may be handed out.
type Checks struct {
	Storage Storage
	cache   cache[[]Check]
}

// Get returns the door's checks. ErrNotFound means they were never uploaded.
func (c *Checks) Get(ctx context.Context, door int, now time.Time) ([]Check, error) {
	return c.cache.get(door, now, func() ([]Check, error) {
		data, err := readAll(ctx, c.Storage, ChecksName(door))
		if err != nil {
			return nil, err
		}
		checks, err := ParseChecks(data)
		if err != nil {
			return nil, fmt.Errorf("door %d: %w", door, err)
		}
		return checks, nil
	})
}

// Goals checks that every goal in checks is a line of the mission, which has lines lines.
func Goals(checks []Check, lines int) error {
	for _, c := range checks {
		if c.Goal > lines {
			return fmt.Errorf("checks.yaml: goal %d, but page.yaml's mission has %d lines", c.Goal, lines)
		}
	}
	return nil
}
