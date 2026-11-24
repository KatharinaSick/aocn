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

// Check is one check from a door's checks.yaml. Exactly one of Ready, HTTP, Job and Login is
// set.
// The CLI runs them, and sends back one result per ID.
//
//   - ready: [apps, db]        every Deployment in the namespaces is ready. Ready checks run
//     first, and if one fails, the other checks don't run.
//   - http: {method, to, from, expect, data}   a request from a workload
//   - job: apps/cronjob/cleanup                a fresh run of the CronJob completes
//   - login: {issuer, client, secret, from, method, to, expect}   a workload logs in at an
//     OpenID Connect issuer, with a secret or with its ServiceAccount token, and then, if
//     method and to are given, sends a request with the access token
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
	// Login is new in CLI 0.9.0. Older CLIs refuse a kind they don't know, so they say to
	// update instead of running the check without logging in. omitempty keeps the IDs of
	// checks without it as they were.
	Login *LoginCheck `yaml:"login,omitempty" json:"login,omitempty"`
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

// LoginCheck logs in from a workload, and passes if the login, or the request sent with its
// access token, gets the expected answer. Without Secret, the workload logs in with its
// ServiceAccount token. The CLI documents what each field accepts; the API checks they're
// there.
type LoginCheck struct {
	Issuer string `yaml:"issuer" json:"issuer"`
	Client string `yaml:"client" json:"client"`
	Secret string `yaml:"secret,omitempty" json:"secret,omitempty"`
	From   string `yaml:"from" json:"from"`
	Method string `yaml:"method,omitempty" json:"method,omitempty"`
	To     string `yaml:"to,omitempty" json:"to,omitempty"`
	Expect string `yaml:"expect" json:"expect"`
}

var (
	methodPattern = regexp.MustCompile(`^[A-Z]+$`)
	expectPattern = regexp.MustCompile(`^(blocked|[1-5]xx|[1-5][0-9]{2})$`)
	// A login's issuer and to, like the CLI reads them: NAMESPACE/SERVICE[:PORT][/PATH]. The
	// issuer is written as tokens name it: no trailing slash, no query.
	targetPattern = regexp.MustCompile(`^[^/\s:]+/[^/\s:]+(:[0-9]{1,5})?(/\S*)?$`)
	issuerPattern = regexp.MustCompile(`^[^/\s:]+/[^/\s:]+(:[0-9]{1,5})?(/[^\s?#]*[^/\s?#])?$`)
	// A login's from: NAMESPACE/new-pod, NAMESPACE/deploy/NAME or NAMESPACE/cronjob/NAME.
	sourcePattern = regexp.MustCompile(`^[^/\s]+/(new-pod|(deploy|deployment|cronjob)/[^/\s]+)$`)
	// What a login without a request can answer: a token, or a refusal.
	loginOnlyExpectPattern = regexp.MustCompile(`^(blocked|2xx|200)$`)
)

// isTestServer reports whether a target names the CLI's test server, which a login can't use.
func isTestServer(target string) bool {
	return target == "test-server" || strings.HasPrefix(target, "test-server/")
}

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
		case e.Ready != nil || e.HTTP != nil || e.Job != "" || e.Login != nil:
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
	if c.Login != nil {
		kinds++
		l := c.Login
		switch {
		case l.Issuer == "":
			return errors.New("login: issuer is required")
		case !issuerPattern.MatchString(l.Issuer) || isTestServer(l.Issuer):
			return fmt.Errorf("login: issuer must be NAMESPACE/SERVICE/PATH without a trailing slash or a query, got %q", l.Issuer)
		case l.Client == "":
			return errors.New("login: client is required")
		case l.From == "":
			return errors.New("login: from is required")
		case !sourcePattern.MatchString(l.From):
			return fmt.Errorf("login: from must be NAMESPACE/deploy/NAME, NAMESPACE/cronjob/NAME or NAMESPACE/new-pod, got %q", l.From)
		case !expectPattern.MatchString(l.Expect):
			return fmt.Errorf("login: expect must be a status code like 403, a class like 2xx, or blocked, got %q", l.Expect)
		case (l.Method == "") != (l.To == ""):
			return errors.New("login: method and to come together")
		case l.To == "" && !loginOnlyExpectPattern.MatchString(l.Expect):
			return fmt.Errorf("login: a login without a request can only expect 2xx, 200 or blocked, got %q", l.Expect)
		case l.Method != "" && !methodPattern.MatchString(l.Method):
			return fmt.Errorf("login: method must be like GET or POST, got %q", l.Method)
		case l.To != "" && (!targetPattern.MatchString(l.To) || isTestServer(l.To)):
			return fmt.Errorf("login: to must be NAMESPACE/SERVICE[:PORT]/PATH, got %q", l.To)
		}
	}
	if kinds != 1 {
		return errors.New("needs exactly one of ready, http, job or login")
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
