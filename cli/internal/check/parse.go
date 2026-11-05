package check

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Source is where a request comes from.
type Source struct {
	Namespace string
	Kind      string // KindDeployment, KindCronJob or KindNewPod
	Name      string // empty for KindNewPod
}

const (
	KindDeployment = "deploy"
	KindCronJob    = "cronjob"
	KindNewPod     = "new-pod"
)

// ParseSource reads NAMESPACE/deploy/NAME, NAMESPACE/cronjob/NAME or NAMESPACE/new-pod.
func ParseSource(s string) (Source, error) {
	parts := strings.Split(s, "/")
	switch {
	case len(parts) == 2 && parts[0] != "" && parts[1] == KindNewPod:
		return Source{Namespace: parts[0], Kind: KindNewPod}, nil
	case len(parts) == 3 && parts[0] != "" && parts[2] != "":
		switch parts[1] {
		case "deploy", "deployment":
			return Source{Namespace: parts[0], Kind: KindDeployment, Name: parts[2]}, nil
		case "cronjob":
			return Source{Namespace: parts[0], Kind: KindCronJob, Name: parts[2]}, nil
		}
	}
	return Source{}, fmt.Errorf("workload must be NAMESPACE/deploy/NAME, NAMESPACE/cronjob/NAME or NAMESPACE/new-pod, got %q", s)
}

// Describe names the source in a check's output. A CronJob isn't named, so finding it
// stays part of the challenge.
func (s Source) Describe() string {
	switch s.Kind {
	case KindCronJob:
		return "a scheduled job in " + s.Namespace
	case KindNewPod:
		return "a new pod in " + s.Namespace
	default:
		return s.Name
	}
}

// Target is the service a request goes to.
type Target struct {
	Namespace string
	Service   string
	Port      int
	Path      string
	// TestServer is a server the check creates in a namespace of its own, with no policies,
	// so only the sender's policies decide. Its Namespace is set when it's created.
	TestServer bool
}

// TestServerTarget is how a check names the test server: test-server[/PATH].
const TestServerTarget = "test-server"

// defaultPort is the port every station role listens on.
const defaultPort = 8080

// ParseTarget reads NAMESPACE/SERVICE[:PORT]/PATH or test-server[/PATH]. The path is
// optional.
func ParseTarget(s string) (Target, error) {
	if path, ok := strings.CutPrefix(s, TestServerTarget); ok && (path == "" || path[0] == '/') {
		path = "/" + strings.TrimPrefix(path, "/")
		return Target{Service: TestServerTarget, Port: defaultPort, Path: path, TestServer: true}, nil
	}
	parts := strings.SplitN(s, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Target{}, fmt.Errorf("target must be NAMESPACE/SERVICE/PATH, got %q", s)
	}
	t := Target{Namespace: parts[0], Service: parts[1], Port: defaultPort, Path: "/"}
	if name, port, ok := strings.Cut(parts[1], ":"); ok {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 || name == "" {
			return Target{}, fmt.Errorf("target must be NAMESPACE/SERVICE:PORT/PATH, got %q", s)
		}
		t.Service, t.Port = name, p
	}
	if len(parts) == 3 {
		t.Path = "/" + parts[2]
	}
	return t, nil
}

// URL is the address of the target inside the cluster.
func (t Target) URL() string {
	return fmt.Sprintf("http://%s.%s:%d%s", t.Service, t.Namespace, t.Port, t.Path)
}

// Expect is the result an HTTP check passes on.
type Expect struct {
	Blocked bool
	Status  int // an exact status code, like 403
	Class   int // a status class, like 2 for 2xx
}

var classPattern = regexp.MustCompile(`^([1-5])xx$`)

// ParseExpect reads a status code (403), a status class (2xx) or "blocked".
func ParseExpect(s string) (Expect, error) {
	if s == "blocked" {
		return Expect{Blocked: true}, nil
	}
	if m := classPattern.FindStringSubmatch(s); m != nil {
		return Expect{Class: int(m[1][0] - '0')}, nil
	}
	if code, err := strconv.Atoi(s); err == nil && code >= 100 && code <= 599 {
		return Expect{Status: code}, nil
	}
	return Expect{}, fmt.Errorf("expect must be a status code like 403, a class like 2xx, or blocked, got %q", s)
}

// Matches reports whether a probe's answer is the expected one. The probe prints the status
// ("200 OK") or "error: ..." if the request didn't get through.
//
// Blocked passes on a 403 or no connection. A network can answer an HTTP-level deny with a
// 403 or drop the connection, and both are valid solutions.
func (e Expect) Matches(answer string) bool {
	if e.Blocked && strings.HasPrefix(answer, "error:") {
		return true
	}
	status, _, _ := strings.Cut(answer, " ")
	code, err := strconv.Atoi(status)
	if err != nil {
		return false
	}
	switch {
	case e.Blocked:
		return code == 403
	case e.Class != 0:
		return code/100 == e.Class
	default:
		return code == e.Status
	}
}

// Describe says what the check expects, as a pass and a fail message about what.
func (e Expect) Describe(what string) (pass, fail string) {
	switch {
	case e.Blocked:
		return what + " is blocked", what + " should be blocked"
	case e.Class == 2:
		return what + " works", what + " should work"
	case e.Class != 0:
		return fmt.Sprintf("%s answers %dxx", what, e.Class), fmt.Sprintf("%s should answer %dxx", what, e.Class)
	default:
		return fmt.Sprintf("%s answers %d", what, e.Status), fmt.Sprintf("%s should answer %d", what, e.Status)
	}
}
