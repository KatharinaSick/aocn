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
// The CLI runs them, in order, and sends back one result per ID.
//
//   - ready: [apps, db]        every Deployment in the namespaces is ready. If not, the
//     checks after it don't run.
//   - http: {method, to, from, expect, data}   a request from a workload
//   - job: apps/cronjob/cleanup                a fresh run of the CronJob completes
type Check struct {
	// ID is a hash of everything else, so a result names exactly the check it's for. Editing
	// a check changes its ID.
	ID    string     `yaml:"-" json:"id,omitempty"`
	Ready []string   `yaml:"ready,omitempty" json:"ready,omitempty"`
	HTTP  *HTTPCheck `yaml:"http,omitempty" json:"http,omitempty"`
	Job   string     `yaml:"job,omitempty" json:"job,omitempty"`
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
	var checks []Check
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&checks); err != nil {
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
	if len(checks) == 0 {
		return nil, errors.New("checks.yaml has no checks")
	}

	seen := map[string]int{}
	for i := range checks {
		c := &checks[i]
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("checks.yaml: check %d: %w", i+1, err)
		}
		c.ID = c.hash()
		if first, ok := seen[c.ID]; ok {
			return nil, fmt.Errorf("checks.yaml: check %d is the same as check %d", i+1, first)
		}
		seen[c.ID] = i + 1
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

// hash is the check's ID: the first 16 hex digits of the SHA-256 of its JSON without the ID.
// encoding/json writes struct fields in a fixed order, so the same check always hashes the
// same.
func (c Check) hash() string {
	c.ID = ""
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
